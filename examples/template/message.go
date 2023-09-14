package template

import (
	as "github.com/godevsig/adaptiveservice"
)

// Request is the request from clients.
// Return *Reply
type Request struct {
	Input string
}

// Reply is the reply for Request to clients
type Reply struct {
	Output string
}

func init() {
	as.RegisterType((*Request)(nil))
	as.RegisterType((*Reply)(nil))
}

//go:generate mkdir -p $GOPACKAGE
//go:generate sh -c "echo '// Code generated by original message.go. DO NOT EDIT.\n' > $GOPACKAGE/$GOFILE"
//go:generate sh -c "grep -v go:generate $GOFILE >> $GOPACKAGE/$GOFILE"
//go:generate gofmt -w $GOPACKAGE/$GOFILE
//go:generate git add $GOPACKAGE/$GOFILE
