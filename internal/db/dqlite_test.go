package db

import (
	"context"
	"io"
	"net"
	"time"
)

// Ensures Accept closes connections right away once the accept channel is closed for good,
// and hands them to the dqlite app again once a new accept channel is opened.
func (s *dbSuite) TestAcceptClosedChannel() {
	db := &DqliteDB{ctx: context.Background(), acceptCh: make(chan net.Conn)}
	db.closeAcceptCh()

	local, remote := net.Pipe()
	defer remote.Close()

	start := time.Now()
	db.Accept(local)
	s.Less(time.Since(start), time.Second)

	// The remote end sees the connection closed.
	_, err := remote.Read(make([]byte, 1))
	s.ErrorIs(err, io.EOF)

	acceptCh := db.openAcceptCh()
	s.NotNil(acceptCh)

	local, remote = net.Pipe()
	defer remote.Close()

	go db.Accept(local)

	select {
	case conn := <-acceptCh:
		s.Equal(local, conn)
		_ = conn.Close()
	case <-time.After(5 * time.Second):
		s.Fail("Connection was not handed to the accept channel")
	}
}
