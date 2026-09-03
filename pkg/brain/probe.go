package brain

import (
	"context"
	"fmt"
)

// Probe proves the store is reachable and its database answers, read-only. The
// note also says which half of the service this machine has: reads always,
// writes only with the token — "read-only" here is a configuration fact, not a
// failure, so it belongs on the OK line rather than turning anything red.
func Probe(ctx context.Context, cfg Config) (string, error) {
	c := New(cfg)
	h, err := c.Healthz(ctx)
	if err != nil {
		return "", err
	}
	if !h.OK {
		return "", fmt.Errorf("the store at %s cannot reach its database", c.BaseURL())
	}
	mode := "token present"
	if !c.CanWrite() {
		mode = "no token — writes refused, reads only if this store serves public ones"
	}
	return fmt.Sprintf("%s — %d documents, %s", c.BaseURL(), h.Docs, mode), nil
}
