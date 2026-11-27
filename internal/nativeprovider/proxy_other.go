//go:build !linux

package nativeprovider

import "context"

type Proxy struct{}
func New(Options) (*Proxy,error) { return nil,ErrClosed }
func (*Proxy) Exchange(context.Context,Request,*Request) (Response,error) { return Response{},ErrClosed }
