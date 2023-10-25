package myTest

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/lucas-clemente/quic-go"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

const (
	rAddr   = "localhost:4242"
	message = "quic-go test"
)

const OneYear = time.Second * 60 * 60 * 24 * 365

func TestClient(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}
	quicCfg := &quic.Config{CreatePaths: true, KeepAlive: true}
	conn, err := quic.DialAddr(rAddr, tlsConf, quicCfg)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := conn.OpenStreamSync()
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Client: Sending '%s'\n", message)
	_, err = stream.Write([]byte(message))
	if err != nil {
		t.Fatal(err)
	}

	readSuccess := make(chan bool)
	go func() {
		buf := make([]byte, len(message))
		_, err = io.ReadFull(stream, buf)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("Client: Got '%s'\n", buf)
		<-readSuccess
	}()

	select {
	case <-c:
	case <-ctx.Done():
	case readSuccess <- true:
	}
}
