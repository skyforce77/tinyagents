package actor

// Terminated is delivered to every watcher of an actor once that actor has
// fully stopped. Reason is non-nil only when termination followed a failure
// (panic or returned error) that the supervisor could not recover from.
type Terminated struct {
	PID    PID
	Reason any
}

// Failed is delivered to a parent actor when one of its children fails with
// a directive of Escalate. The parent can react to it in Receive.
type Failed struct {
	Child PID
	Cause any
}
