package pool

import "context"

// Retain local pressure across global/regional pool replacements and reloads.
var processResourceBackoff resourceBackoff

func (p *poolOutbound) checkResourceBackoff(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.resourceBackoff.check()
}

func (p *poolOutbound) pauseForLocalResourceError(err error) bool {
	return p.resourceBackoff.observe(err)
}
