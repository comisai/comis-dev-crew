package comiswire

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// gatedControlConnection keeps a caller inside one write until the test releases it, so the
// session state that the caller observes afterwards is arranged deterministically.
type gatedControlConnection struct {
	net.Conn
	written  chan struct{}
	released chan struct{}
	once     sync.Once
}

func (connection *gatedControlConnection) Write(contents []byte) (int, error) {
	connection.once.Do(func() { close(connection.written) })
	<-connection.released
	return len(contents), nil
}

func TestControlSessionCallPrefersDeliveredResponseOverLaterConnectionFailure(t *testing.T) {
	const operationID = OperationID("operation_attention_race_a")
	const managedRunID = ManagedRunID("managed-run_race_a")
	const externalKey = "database-choice"
	private := "Use the existing PostgreSQL adapter."
	// A routed response is a known outcome. A connection failure observed in the same instant
	// must not turn it into an uncertain one. One attempt cannot prove the preference because a
	// select chooses uniformly among ready cases, so the invariant is asserted repeatedly.
	for attempt := 0; attempt < 64; attempt++ {
		client, peer := net.Pipe()
		gated := &gatedControlConnection{
			Conn: client, written: make(chan struct{}), released: make(chan struct{}),
		}
		session := newControlSession(gated, controlTestBearer, nil, time.Second)
		var response ReceiveAttentionResponseResponse
		results := make(chan error, 1)
		go func() {
			results <- session.call(context.Background(), ReceiveAttentionResponseRequest{
				JSONRPC: JSONRPCVersion, ID: operationID,
				Method: MethodManagedRunsReceiveAttentionResponse,
				Params: ReceiveAttentionResponseRequestParams{
					ExternalKey: externalKey, ManagedRunID: managedRunID, OperationID: operationID,
				},
			}, operationID, &response)
		}()
		<-gated.written
		frame, err := json.Marshal(ReceiveAttentionResponseResponse{
			JSONRPC: JSONRPCVersion, ID: operationID,
			Result: ReceiveAttentionResponseResponseResult{
				ExternalKey: externalKey, ManagedRunID: managedRunID,
				State: ManagedRunStateDelivered, Response: &private,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := session.route(context.Background(), frame); err != nil {
			t.Fatalf("attempt %d: route() error = %v", attempt, err)
		}
		session.fail(io.EOF)
		close(gated.released)
		if err := <-results; err != nil {
			t.Fatalf("attempt %d: call() error = %v", attempt, err)
		}
		if response.Result.State != ManagedRunStateDelivered || response.Result.Response == nil ||
			*response.Result.Response != private {
			t.Fatalf("attempt %d: call() response = %#v", attempt, response.Result)
		}
		_ = peer.Close()
		_ = client.Close()
	}
}

func TestControlSessionCallReportsFailureWhenNoResponseWasDelivered(t *testing.T) {
	const operationID = OperationID("operation_attention_race_b")
	client, peer := net.Pipe()
	defer peer.Close()
	defer client.Close()
	gated := &gatedControlConnection{
		Conn: client, written: make(chan struct{}), released: make(chan struct{}),
	}
	session := newControlSession(gated, controlTestBearer, nil, time.Second)
	var response ReceiveAttentionResponseResponse
	results := make(chan error, 1)
	go func() {
		results <- session.call(context.Background(), ReceiveAttentionResponseRequest{
			JSONRPC: JSONRPCVersion, ID: operationID,
			Method: MethodManagedRunsReceiveAttentionResponse,
			Params: ReceiveAttentionResponseRequestParams{
				ExternalKey: "database-choice", ManagedRunID: "managed-run_race_b", OperationID: operationID,
			},
		}, operationID, &response)
	}()
	<-gated.written
	session.fail(io.EOF)
	close(gated.released)
	if err := <-results; err != io.EOF {
		t.Fatalf("call() error = %v, want %v", err, io.EOF)
	}
}
