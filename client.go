package bionic

import "fmt"

type HandlerFunc func([]byte) error

type Client struct {
	handlers []HandlerFunc
}

func NewClient() *Client {
	return &Client{handlers: []HandlerFunc{}}
}

func (c *Client) RegisterHandlers(f ...HandlerFunc) {
	c.handlers = append(c.handlers, f...)
}

func (c *Client) handle(payload []byte) {
	for _, c := range c.handlers {
		if err := c(payload); err != nil {
			fmt.Printf(err.Error())
		}
	}
}
