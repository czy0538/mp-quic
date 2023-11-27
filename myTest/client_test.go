package myTest

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/lucas-clemente/quic-go"
	"io"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	rAddr = "192.168.1.191:4242"
	//rAddr   = "100.64.1.17:4242"
	message = "quic-go test"
)

var streamNums = 10

const OneYear = time.Second * 60 * 60 * 24 * 365

func TestClient(t *testing.T) {

	flag.IntVar(&streamNums, "n", 5, "stream nums")
	flag.Parse()

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

	time.Sleep(5 * time.Second)
	wg := sync.WaitGroup{}
	wg.Add(streamNums)
	for i := 0; i < streamNums; i++ {
		go func(i int) {
			stream, err := conn.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("Client %d: Sending '%s'\n", i, message)
			_, err = stream.Write([]byte(strconv.Itoa(i) + message))
			if err != nil {
				t.Fatal(err)
			}

			go func() {
				buf := make([]byte, len(message)+1)
				_, err = io.ReadFull(stream, buf)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("Client %d: Got '%s'\n", i, buf)
				wg.Done()

			}()
		}(i)
	}
	wg.Wait()
	time.Sleep(10 * time.Second)
	t.Log("create new streams ")
	wg.Add(streamNums)
	for i := 0; i < streamNums; i++ {
		go func(i int) {
			stream, err := conn.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("Client %d: Sending '%s'\n", i, message)
			_, err = stream.Write([]byte(strconv.Itoa(i) + message))
			if err != nil {
				t.Fatal(err)
			}

			go func() {
				buf := make([]byte, len(message)+1)
				_, err = io.ReadFull(stream, buf)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("Client %d: Got '%s'\n", i, buf)
				wg.Done()

			}()
		}(i)
	}

	select {
	case <-c:
	case <-ctx.Done():
	}
}
