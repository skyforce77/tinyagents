package actor

// PID uniquely identifies an actor across a cluster. In v0.1 (single-node)
// Node is always the local System's name; clustering later populates it with
// the owning node's identifier.
type PID struct {
	Node string
	Path string
}

func (p PID) String() string {
	if p.Node == "" {
		return p.Path
	}
	return p.Node + p.Path
}
