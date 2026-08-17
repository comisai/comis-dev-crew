package application

type dependencyFailure struct {
	message string
	cause   error
}

func (failure *dependencyFailure) Error() string { return failure.message }
func (failure *dependencyFailure) Unwrap() error { return failure.cause }

// SafeDependencyMessage returns the bounded application-owned stage without
// exposing the wrapped adapter error across a transport boundary.
func (failure *dependencyFailure) SafeDependencyMessage() string { return failure.message }
