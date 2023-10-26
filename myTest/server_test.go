package myTest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/lucas-clemente/quic-go"
	"io"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

const lAddr = "0.0.0.0:4242"

func TestServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	quicCfg := &quic.Config{CreatePaths: true, KeepAlive: true}
	listener, err := quic.ListenAddr(lAddr, generateTLSConfig(), quicCfg)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:

			}
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			go func(conn quic.Session) {
				for {
					select {
					case <-ctx.Done():
						return
					default:

					}
					fmt.Printf("get a new connection, remote addr: %s\n", conn.RemoteAddr().String())
					stream, err := conn.AcceptStream()
					if err != nil {
						t.Fatal(err)
					}
					go func(stream quic.Stream) {
						fmt.Printf("Server: Got a new stream %d\n", stream.StreamID())
						// Echo through the loggingWriter
						_, err = io.Copy(loggingWriter{stream}, stream)
					}(stream)
				}
			}(conn)

		}
	}()

	select {
	case <-c:
		os.Exit(0)
	case <-ctx.Done():
		os.Exit(0)
	}
}

// A wrapper for io.Writer that also logs the message.
type loggingWriter struct{ io.Writer }

func (w loggingWriter) Write(b []byte) (int, error) {
	fmt.Printf("Server: Got '%s'\n", string(b))
	return w.Writer.Write(b)
}

// Setup a bare-bones TLS config for the server
func generateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"quic-echo-example"},
	}
}
