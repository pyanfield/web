package web

import (
	"net"
	"net/http/fcgi"
)

// CGI全称是“公共网关接口”(Common Gateway Interface)，HTTP服务器与你的或其它机器上的程序进行“交谈”的一种工具
// FastCGI是语言无关的、可伸缩架构的CGI开放扩展，其主要行为是将CGI解释器进程保持在内存中并因此获得较高的性能。
func (s *Server) listenAndServeFcgi(addr string) error {
	var l net.Listener
	var err error

	//if the path begins with a "/", assume it's a unix address
	if addr[0] == '/' {
		l, err = net.Listen("unix", addr)
	} else {
		l, err = net.Listen("tcp", addr)
	}

	//save the listener so it can be closed
	s.l = l

	if err != nil {
		s.Logger.Println("FCGI listen error", err.Error())
		return err
	}
	return fcgi.Serve(s.l, s)
}
