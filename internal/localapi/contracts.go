package localapi

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Operator contracts arrive as bounded JSON from a file or standard input rather
// than on the command line. They carry only the operator's own words: the task
// and the question travel separately, so a contract cannot redirect an
// instruction or an answer at a subject the operator did not name.

// SteerTaskContract carries one bounded steering instruction. The task travels
// on the command line, so a contract cannot redirect an instruction at a
// different task than the one the operator named.
type SteerTaskContract struct {
	SchemaVersion int    `json:"schemaVersion"`
	Instruction   string `json:"instruction"`
}

// DecodeSteerTaskInput reads one strict bounded steering contract.
func DecodeSteerTaskInput(data []byte) (SteerTaskContract, error) {
	var input SteerTaskContract
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return SteerTaskContract{}, errors.New("steer input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return SteerTaskContract{}, err
	}
	if input.SchemaVersion != 1 {
		return SteerTaskContract{}, errors.New("steer schemaVersion must equal 1")
	}
	if err := domain.ValidateSteeringInstruction(input.Instruction); err != nil {
		return SteerTaskContract{}, err
	}
	return input, nil
}

// RespondDecisionContract is the operator-supplied half of one answer. It
// carries only the reply: the task and the question travel on the command line,
// so a contract cannot redirect an answer at a different question than the one
// the operator named.
type RespondDecisionContract struct {
	SchemaVersion int    `json:"schemaVersion"`
	Response      string `json:"response"`
}

// DecodeRespondDecisionInput reads one strict bounded answer contract.
func DecodeRespondDecisionInput(data []byte) (RespondDecisionContract, error) {
	var input RespondDecisionContract
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return RespondDecisionContract{}, errors.New("decision answer input exceeds its bound")
	}
	if err := decodeObject(data, &input); err != nil {
		return RespondDecisionContract{}, err
	}
	if input.SchemaVersion != 1 {
		return RespondDecisionContract{}, errors.New("decision answer schemaVersion must equal 1")
	}
	if err := domain.ValidateDecisionResponse(input.Response); err != nil {
		return RespondDecisionContract{}, err
	}
	return input, nil
}
