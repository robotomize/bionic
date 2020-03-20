package main

import (
	"bionic"
	"bionic/clients/gobionic"
)

func Add(bytes []byte) error {
	//kind := uint8(gjson.GetBytes(bytes, "proto.kind").Int())
	//switch kind {
	//case bionic.PingKind:
	//
	//case bionic.NewJobKind:
	//}
}

func main() {
	server := bionic.New()
	_ = server
	client := gobionic.NewClient()

	client.RegisterHandlers(Add)
}
