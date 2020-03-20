package goclient

import "fmt"

type BClientHandlerFunc func([]byte) error

type Client struct {
	handlers []BClientHandlerFunc
}

func NewClient() *Client {
	return &Client{handlers: []BClientHandlerFunc{}}
}

func (c *Client) RegisterHandlers(f ...BClientHandlerFunc) {
	c.handlers = append(c.handlers, f...)
}

func (c *Client) handle(payload []byte) {
	for _, c := range c.handlers {
		if err := c(payload); err != nil {
			fmt.Printf(err.Error())
		}
	}
}
