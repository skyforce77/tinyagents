package llm

// Middleware wraps a Provider and returns a new Provider. The returned
// value delegates to next, possibly adding retry, logging, rate limiting,
// or other concerns before or after the call.
//
// Compose with With: the first middleware is outermost.
//
//	p := llm.With(base,
//	    llm.Log(logger),
//	    llm.Retry(3, llm.ExponentialBackoff(50*time.Millisecond, time.Second), nil),
//	    llm.RateLimit(time.Second, 5),
//	)
type Middleware func(Provider) Provider

// With applies mws to base from right to left so the first Middleware
// passed wraps outermost (called first on the way in, last on the way out).
func With(base Provider, mws ...Middleware) Provider {
	out := base
	for i := len(mws) - 1; i >= 0; i-- {
		out = mws[i](out)
	}
	return out
}
