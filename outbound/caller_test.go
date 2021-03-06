package outbound

import (
	"fmt"
	"github.com/agiledragon/gomonkey"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	mock "github.com/wolf-joe/ts-dns/core/mocker"
	"golang.org/x/net/proxy"
	"io/ioutil"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

var dialer, _ = proxy.SOCKS5("tcp", "", nil, proxy.Direct)

func assertFail(t *testing.T, val *dns.Msg, err error) {
	assert.Nil(t, val)
	assert.NotNil(t, err)
}
func assertSuccess(t *testing.T, val *dns.Msg, err error) {
	assert.NotNil(t, val)
	assert.Nil(t, err)
}

func MockMethodSeq(target interface{}, methodName string, outputs []gomonkey.Params) *gomonkey.Patches {
	var cells []gomonkey.OutputCell
	for _, output := range outputs {
		cells = append(cells, gomonkey.OutputCell{Values: output})
	}
	return gomonkey.ApplyMethodSeq(reflect.TypeOf(target), methodName, cells)
}

func TestDNSCaller(t *testing.T) {
	req := &dns.Msg{}

	caller := NewDNSCaller("", "", nil)
	// 不使用代理，mock掉Exchange
	p := MockMethodSeq(caller.client, "Exchange", []gomonkey.Params{
		{nil, time.Second, fmt.Errorf("err")},
		{&dns.Msg{}, time.Second, nil},
	})
	// exchange调用失败
	r, err := caller.Call(req)
	assertFail(t, r, err)
	// exchange调用成功
	r, err = caller.Call(req)
	assertSuccess(t, r, err)

	caller = NewDoTCaller("", "", dialer)
	// 使用代理，mock掉Dial、WriteMsg、ReadMsg
	p1 := MockMethodSeq(caller.proxy, "Dial", []gomonkey.Params{
		{nil, fmt.Errorf("err")},
		{&net.TCPConn{}, nil}, {&net.TCPConn{}, nil}, {&net.TCPConn{}, nil},
	})
	p2 := MockMethodSeq(caller.conn, "WriteMsg", []gomonkey.Params{
		{fmt.Errorf("err")}, {nil}, {nil},
	})
	p3 := MockMethodSeq(caller.conn, "ReadMsg", []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {&dns.Msg{}, nil},
	})
	defer func() { p.Reset(); p1.Reset(); p2.Reset(); p3.Reset() }()
	// Dial失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Dial成功，但WriteMsg失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Dial、WriteMsg成功，但ReadMsg失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Dial、WriteMsg、ReadMsg都成功
	r, err = caller.Call(req)
	assertSuccess(t, r, err)
}

func TestDoHCaller(t *testing.T) {
	mocker := mock.Mocker{}
	defer mocker.Reset()

	req := &dns.Msg{}
	httpReq := &http.Request{Header: map[string][]string{}}

	// 测试NewDoHCaller
	_, err := NewDoHCaller("%%%%", dialer) // url解析失败
	assert.NotNil(t, err)
	_, err = NewDoHCaller("", dialer) // url解析失败
	assert.NotNil(t, err)
	_, err = NewDoHCaller("https://:::/", dialer) // url解析失败
	assert.NotNil(t, err)
	caller, err := NewDoHCaller("https://host/path", nil) // url解析成功
	assert.Nil(t, err)
	assert.NotNil(t, caller)
	assert.Equal(t, caller.Host, "host")
	assert.Equal(t, caller.port, "443")
	caller, err = NewDoHCaller("https://host:80/path", dialer) // url解析成功
	assert.Nil(t, err)
	assert.NotNil(t, caller)
	assert.Equal(t, caller.port, "80")
	// 测试.Resolve
	mocker.FuncSeq(net.LookupIP, []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {[]net.IP{nil}, nil}, {[]net.IP{{1, 1, 1, 1}}, nil},
	})
	err = caller.Resolve() // LookupIP返回异常
	assert.NotNil(t, err)
	err = caller.Resolve() // LookupIP返回IP列表异常
	assert.NotNil(t, err)
	err = caller.Resolve() // LookupIP返回1.1.1.1
	assert.Nil(t, err)
	assert.Equal(t, caller.Servers[0], "1.1.1.1")
	// 测试DialContext
	_, _ = caller.client.Transport.(*http.Transport).DialContext(nil, "", "")

	// 测试.Call
	caller.Servers = []string{}
	_, err = caller.Call(req) // Servers为空则返回异常
	assert.NotNil(t, err)

	caller.Servers = []string{"1.1.1.1"}
	mocker.MethodSeq(req, "PackBuffer", []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {[]byte{1}, nil}, {[]byte{1}, nil},
		{[]byte{1}, nil}, {[]byte{1}, nil}, {[]byte{1}, nil},
	})
	mocker.FuncSeq(http.NewRequest, []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {httpReq, nil}, {httpReq, nil},
		{httpReq, nil}, {httpReq, nil},
	})
	mocker.MethodSeq(caller.client, "Do", []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {&http.Response{Body: &net.TCPConn{}}, nil},
		{&http.Response{Body: &net.TCPConn{}}, nil},
		{&http.Response{Body: &net.TCPConn{}}, nil},
	})
	mocker.FuncSeq(ioutil.ReadAll, []gomonkey.Params{
		{nil, fmt.Errorf("err")}, {make([]byte, 1), nil},
		{make([]byte, 12), nil},
	})

	// Pack失败
	r, err := caller.Call(req)
	assertFail(t, r, err)
	// Pack成功，但NewRequest失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Pack、NewRequest成功，但Do失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Pack、NewRequest、Do成功，但ReadAll失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Pack、NewRequest、Do、ReadAll成功，但Unpack失败
	r, err = caller.Call(req)
	assertFail(t, r, err)
	// Pack、NewRequest、Do、ReadAll、Unpack成功
	r, err = caller.Call(req)
	assertSuccess(t, r, err)
}
