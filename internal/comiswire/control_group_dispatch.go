package comiswire

import "context"

func (session *controlSession) dispatchGroup(ctx context.Context, method Method, line []byte) error {
	switch method {
	case MethodManagedRunGroupsActivate:
		var authenticated authenticatedGroupActivateRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid group activation envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "group activation operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.GroupActivateRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid group activation request"))
		}
		result, err := session.handler.GroupActivate(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(
			PayloadGroupActivateResponse,
			GroupActivateResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result},
		)
	case MethodManagedRunGroupsAbandon:
		var authenticated authenticatedGroupAbandonRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid group abandonment envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "group abandonment operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.GroupAbandonRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid group abandonment request"))
		}
		result, err := session.handler.GroupAbandon(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(
			PayloadGroupAbandonResponse,
			GroupAbandonResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result},
		)
	default:
		return session.writeFailure(nil, wireFailure(ErrorKindMethodNotFound, "group method is unsupported"))
	}
}
