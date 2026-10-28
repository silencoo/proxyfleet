package pool

import (
	"net"
	"sync"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

type jobConnectionUseKey struct{}
type jobConnectionUse struct {
	done     func()
	failover *jobs.FailoverAttempt
	job      config.JobConfig
}
type jobConnection struct {
	net.Conn
	done func()
	once sync.Once
}

func (c *jobConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.done)
	return err
}
