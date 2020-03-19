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

func ServeP2P(hub *ConnsHub) {
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

	host.SetStreamHandler("/", hub.handle)
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

type ConnsHub struct {
	out     chan []byte
	closeCh chan struct{}
	cancel  func()
	conns   []*Conn
}

func NewConnsHub() *ConnsHub {
	return &ConnsHub{
		out:     make(chan []byte, 1),
		closeCh: make(chan struct{}, 1),
		conns:   []*Conn{},
	}
}

func (h *ConnsHub) Stop() {
	h.cancel()
}

func (h *ConnsHub) handle(s network.Stream) {
	ctx, cancel := context.WithCancel(context.TODO())
	h.cancel = cancel
	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))
	c := &Conn{
		ID:     s.Conn().LocalPeer().String(),
		buffer: rw,
		out:    h.out,
	}
	c.read(ctx)
	h.conns = append(h.conns, c)
}

func (h *ConnsHub) SendTo(ID string, payload []byte) {
	for _, c := range h.conns {
		if c.ID == ID {
			c.write(payload)
		}
	}
}

func (h *ConnsHub) Register() {

}

func (h *ConnsHub) Unregister() {

}

type Conn struct {
	ID     string
	out    chan []byte
	buffer *bufio.ReadWriter
}

func (c *Conn) read(ctx context.Context) {
	go func() {
		for {
			b, err := ioutil.ReadAll(c.buffer)
			if err != nil {
				fmt.Printf(err.Error())
				return
			}
			c.out <- b
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
}

func (c *Conn) write(msg []byte) {
	_, err := c.buffer.Write(msg)
	if err != nil {
		fmt.Printf(err.Error())
		return
	}
	if err := c.buffer.Flush(); err != nil {
		fmt.Printf(err.Error())
		return
	}
}
