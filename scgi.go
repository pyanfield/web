package web

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"strconv"
	"strings"
)

type scgiBody struct {
	reader io.Reader
	conn   io.ReadWriteCloser
	closed bool
}

func (b *scgiBody) Read(p []byte) (n int, err error) {
	if b.closed {
		return 0, errors.New("SCGI read after close")
	}
	return b.reader.Read(p)
}

func (b *scgiBody) Close() error {
	b.closed = true
	return b.conn.Close()
}

type scgiConn struct {
	fd           io.ReadWriteCloser
	req          *http.Request
	headers      http.Header
	wroteHeaders bool
}

func (conn *scgiConn) WriteHeader(status int) {
	if !conn.wroteHeaders {
		conn.wroteHeaders = true

		var buf bytes.Buffer
		text := statusText[status]

		fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", status, text)

		for k, v := range conn.headers {
			for _, i := range v {
				buf.WriteString(k + ": " + i + "\r\n")
			}
		}

		buf.WriteString("\r\n")
		conn.fd.Write(buf.Bytes())
	}
}

func (conn *scgiConn) Header() http.Header {
	return conn.headers
}

func (conn *scgiConn) Write(data []byte) (n int, err error) {
	if !conn.wroteHeaders {
		conn.WriteHeader(200)
	}

	if conn.req.Method == "HEAD" {
		return 0, errors.New("Body Not Allowed")
	}

	return conn.fd.Write(data)
}

func (conn *scgiConn) Close() { conn.fd.Close() }

func (conn *scgiConn) finishRequest() error {
	var buf bytes.Buffer
	if !conn.wroteHeaders {
		conn.wroteHeaders = true

		for k, v := range conn.headers {
			for _, i := range v {
				buf.WriteString(k + ": " + i + "\r\n")
			}
		}

		buf.WriteString("\r\n")
		conn.fd.Write(buf.Bytes())
	}
	return nil
}

func (s *Server) readScgiRequest(fd io.ReadWriteCloser) (*http.Request, error) {
	// 生成新的 Reader 对象
	reader := bufio.NewReader(fd)
	// 提取第一个冒号之前的部分
	line, err := reader.ReadString(':')
	if err != nil {
		s.Logger.Println("Error during SCGI read: ", err.Error())
	}
	// 计算包头的长度，检测是否已经超过规定的长度
	length, _ := strconv.Atoi(line[0 : len(line)-1])
	if length > 16384 {
		s.Logger.Println("Error: max header size is 16k")
	}
	headerData := make([]byte, length)
	_, err = reader.Read(headerData)
	if err != nil {
		return nil, err
	}

	b, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	// discard the trailing comma
	// 报头和报体是用逗号隔开的，所以这里要检测是都有逗号
	if b != ',' {
		return nil, errors.New("SCGI protocol error: missing comma")
	}
	headerList := bytes.Split(headerData, []byte{0})
	headers := map[string]string{}
	for i := 0; i < len(headerList)-1; i += 2 {
		headers[string(headerList[i])] = string(headerList[i+1])
	}
	httpReq, err := cgi.RequestFromMap(headers)
	if err != nil {
		return nil, err
	}
	if httpReq.ContentLength > 0 {
		httpReq.Body = &scgiBody{
			reader: io.LimitReader(reader, httpReq.ContentLength),
			conn:   fd,
		}
	} else {
		httpReq.Body = &scgiBody{reader: reader, conn: fd}
	}
	return httpReq, nil
}

func (s *Server) handleScgiRequest(fd io.ReadWriteCloser) {
	req, err := s.readScgiRequest(fd)
	if err != nil {
		s.Logger.Println("SCGI error: %q", err.Error())
	}
	sc := scgiConn{fd, req, make(map[string][]string), false}
	s.routeHandler(req, &sc)
	sc.finishRequest()
	fd.Close()
}

// 对符合 SCGI 协议的服务进行监听
func (s *Server) listenAndServeScgi(addr string) error {

	var l net.Listener
	var err error

	//if the path begins with a "/", assume it's a unix address
	// 如果地址是以 "/" 开头，那么我们就按照 unix 地址来对待
	// 否则按照 tcp 的地址对待
	if strings.HasPrefix(addr, "/") {
		l, err = net.Listen("unix", addr)
	} else {
		l, err = net.Listen("tcp", addr)
	}

	//save the listener so it can be closed
	s.l = l

	if err != nil {
		s.Logger.Println("SCGI listen error", err.Error())
		return err
	}

	for {
		fd, err := l.Accept()
		if err != nil {
			s.Logger.Println("SCGI accept error", err.Error())
			return err
		}
		go s.handleScgiRequest(fd)
	}
	return nil
}
