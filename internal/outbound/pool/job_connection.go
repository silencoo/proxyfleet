package pool

import (
	"net"
	"sync"
)

type jobConnectionUseKey struct{}
type jobConnectionUse struct{ done func() }
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
