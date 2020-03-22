# bionic

This is a simple and easy-to-configure system for distributing tasks between multiple network client nodes.

Bionic is made to quickly deploy distributed computing involving user hosts. Websocket is used as a transport.

### install
This version is not for use in production.

``` go get github.com/robotomize/bionic```

A client calculating pi may look like this

``` 
c, err := bionic.NewClient("ws://localhost:9090/ws", http.Header{"Cookie": []string{}})
	if err != nil {
		fmt.Printf(err.Error())
		os.Exit(2)
	}
	c.RegisterHandlers("pi", func(j *bionic.JobMessage) error {
		var req *PiJobReq
		if err := json.Unmarshal(j.Job.Payload, &req); err != nil {
			return err
		}
		n := req.N
		res := &PiJobResp{Pi: pi(n)}

		bytes, err := json.Marshal(&res)
		if err != nil {
			return err
		}
		j.Proto.Kind = bionic.JobCompletedMessageKind
		j.Job.Payload = bytes
		return nil
	})
	go c.Listen()
```
