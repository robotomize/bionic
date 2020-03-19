package bionic

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/multiformats/go-multiaddr"
	"io/ioutil"
)

func ServeP2P() {
	r := rand.Reader
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
	if err != nil {
		panic(err)
	}

	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", DefaultAddr))

	host, err := libp2p.New(
		context.Background(),
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey))

	if err != nil {
		panic(err)
	}

	h := newHandler()
	host.SetStreamHandler("/", h.handle)
	var port string
	for _, la := range host.Network().ListenAddresses() {
		if p, err := la.ValueForProtocol(multiaddr.P_TCP); err == nil {
			port = p
			break
		}
	}

	m := make(chan struct{})
	fmt.Printf("Run './chat -d /ip4/127.0.0.1/tcp/%v/p2p/%s'\n", port, host.ID().Pretty())
	fmt.Printf("\nWaiting for incoming connection\n\n")
	<-m
}

type Handler struct {
	in      chan []byte
	out     chan []byte
	closeCh chan struct{}
	cancel  func()
	conns   []*Conn
}

func newHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Stop() {
	h.cancel()
}

func (h *Handler) handle(s network.Stream) {
	ctx, cancel := context.WithCancel(context.TODO())
	h.cancel = cancel
	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))
	c := &Conn{
		ID:  s.Conn().LocalPeer().String(),
		in:  make(chan []byte, 1),
		out: make(chan []byte, 1),
	}
	c.read(ctx, rw)
	c.write(ctx, rw)
	h.conns = append(h.conns, c)
}

func (h *Handler) Register() {

}

func (h *Handler) Unregister() {

}

type Conn struct {
	ID  string
	in  chan []byte
	out chan []byte
}

func (h *Conn) read(ctx context.Context, rw *bufio.ReadWriter) {
	go func() {
		defer func() {
			h.closeCh <- struct{}{}
		}()
		for {
			b, err := ioutil.ReadAll(rw)
			if err != nil {
				fmt.Printf(err.Error())
				return
			}
			h.out <- b
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
}

func (h *Conn) write(ctx context.Context, rw *bufio.ReadWriter) {
	go func() {
		defer func() {
			h.closeCh <- struct{}{}
		}()
		for {
			select {
			case msg := <-h.in:
				n, err := rw.Write(msg)
				if err != nil || n == 0 {
					fmt.Printf(err.Error())
					return
				}
				if err := rw.Flush(); err != nil {
					fmt.Printf(err.Error())
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
