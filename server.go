package web

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

// ServerConfig is configuration for server objects.
// ServerConfig 结构体定义了一些服务器的配置信息
type ServerConfig struct {
	StaticDir    string // 静态文件夹路径
	Addr         string // 服务地址
	Port         int    // 服务端口号
	CookieSecret string // cookie 安全验证
	RecoverPanic bool
	Profiler     bool // 是否进行代码的性能测试
}

// Server represents a web.go server.
// Server 结构体描述了 web.go 的服务器信息
type Server struct {
	Config *ServerConfig // 服务器的配置信息
	routes []route       // 路由
	Logger *log.Logger
	Env    map[string]interface{}
	//save the listener so it can be closed
	l net.Listener // 网络监听器
}

// 创建一个新的 Server 对象，定义 Config, Logger 和 Env 信息。
func NewServer() *Server {
	return &Server{
		// 这里初始化的 Config 信息在 web.go 文件中可见，只是初始化了 RecoverPanic:true
		Config: Config,
		Logger: log.New(os.Stdout, "", log.Ldate|log.Ltime),
		// 创建一个空的 map[string]interface{}
		Env: map[string]interface{}{},
	}
}

// 设置 Server 的 Config 和 Logger 的默认值，如果没有设置就用用默认值代替。
func (s *Server) initServer() {
	if s.Config == nil {
		s.Config = &ServerConfig{}
	}

	if s.Logger == nil {
		s.Logger = log.New(os.Stdout, "", log.Ldate|log.Ltime)
	}
}

// 路由信息
type route struct {
	r       string         // HTTP 请求的地址
	cr      *regexp.Regexp // 路由的正则表达式对象
	method  string         // HTTP 请求的方法
	handler reflect.Value  // 处理函数的值
}

// 为不同的请求添加路由功能，根据不同的请求去响应不同的处理方法
func (s *Server) addRoute(r string, method string, handler interface{}) {
	// 解析正则表达式，如果成功了返回一个正则表达式对象 cr,用于正则匹配
	cr, err := regexp.Compile(r)
	if err != nil {
		s.Logger.Printf("Error in route regex %q\n", r)
		return
	}
	// 检测 handler 是否能够直接转换成 reflect.Value 类型
	// 这里有个判断是因为如果直接对 handler 进行类型转换的话，那么转换失败将产生错误。
	// 所以如果能直接转换，那么就直接转换并保存至 fv,添加到 routes 里。
	// 如果不能就使用 reflect.ValueOf 来获取handler 的 Value 值
	// 这里的 handler.() 这种写法是对 interface{} 对象做类型推断，如果括号中是一个 interface 类型的话，
	// 那么这里做类型推断的时候即使推断出没有实现该借口，也不会产生错误，但是如果括号中是一个数据类型的话，
	// 比如 struct 类型的话，那么类型推断失败，就会产生错误。
	if fv, ok := handler.(reflect.Value); ok {
		s.routes = append(s.routes, route{r, cr, method, fv})
	} else {
		// 获取 handler 方法的 Value 值
		// 比如我们的 handler 是这样的一个函数 func hello(val string) string
		// 那么这个地方 fv 就会返回 <func(string) string Value>
		// 注意 ValueOf(pointer-interface) 返回的是⼀个 Pointer,也就是接口对象保存的 *data 内容.
		// 要 想操作目标对象,需要⽤用 Elem() 进⼀一步获取指针指向的实际目标。
		fv := reflect.ValueOf(handler)
		s.routes = append(s.routes, route{r, cr, method, fv})
	}
}

// ServeHTTP is the interface method for Go's http server package
// 经过 func (s *Server) Run(addr string) 一系列调用之后，调用到这里
func (s *Server) ServeHTTP(c http.ResponseWriter, req *http.Request) {
	s.Process(c, req)
}

// Process invokes the routing system for server s
// 调用路由处理方法
func (s *Server) Process(c http.ResponseWriter, req *http.Request) {
	s.routeHandler(req, c)
}

// Get adds a handler for the 'GET' http method for server s.
func (s *Server) Get(route string, handler interface{}) {
	s.addRoute(route, "GET", handler)
}

// Post adds a handler for the 'POST' http method for server s.
func (s *Server) Post(route string, handler interface{}) {
	s.addRoute(route, "POST", handler)
}

// Put adds a handler for the 'PUT' http method for server s.
func (s *Server) Put(route string, handler interface{}) {
	s.addRoute(route, "PUT", handler)
}

// Delete adds a handler for the 'DELETE' http method for server s.
func (s *Server) Delete(route string, handler interface{}) {
	s.addRoute(route, "DELETE", handler)
}

// Match adds a handler for an arbitrary http method for server s.
func (s *Server) Match(method string, route string, handler interface{}) {
	s.addRoute(route, method, handler)
}

// Run starts the web application and serves HTTP requests for s
// 开始运行 server，并且去响应 HTTP 的请求
// 这个地方可以对应 Go 的net/http包下的server.go文件来看
func (s *Server) Run(addr string) {
	// 初始化 Server
	s.initServer()
	// 创建一个 ServeMux 对象，其中 ServeMux 是一个HTTP请求的多路转换器。
	// type ServeMux struct {
	//    	mu sync.RWMutex   		//锁，由于请求涉及到并发处理，因此这里需要一个锁机制
	//    	m  map[string]muxEntry  // 路由规则，一个string对应一个mux实体，这里的string就是注册的路由表达式
	// }
	// 	type muxEntry struct {
	//     explicit bool   			// 是否精确匹配
	//     h        Handler 		// 这个路由表达式对应哪个handler
	// }
	mux := http.NewServeMux()
	if s.Config.Profiler {
		mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	}
	// Handle registers the handler for the given pattern.
	// If a handler already exists for pattern, Handle panics.
	// 将我们创建的 Server 对象 s 注册到模型 "/" 中
	// 向 ServeMux的map[string]muxEntry中增加对应的handler和路由规则
	// func (mux *ServeMux) Handle(pattern string, handler Handler)
	// 我们的的 Server 对象 s 实现了 Handler 的 ServeHTTP 方法
	// ServeMux{mu:sync.RWMutex, m:{"/":{explicit:true, h:s}}}
	// mux.m["/"] = muxEntry{explicit:true, h:s}
	mux.Handle("/", s)

	s.Logger.Printf("web.go serving %s\n", addr)
	// 用 TCP 协议搭建一个服务，然后监听设置的端口
	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("ListenAndServe:", err)
	}
	s.l = l
	// Serve accepts incoming HTTP connections on the listener l,
	// creating a new service thread for each.  The service threads
	// read requests and then call handler to reply to them.
	// Handler is typically nil, in which case the DefaultServeMux is used.
	// 参见 $GOROOT/src/pkg/net/http/server.go
	// func Serve(l net.Listener, handler Handler) error {
	// 		srv := &Server{Handler: handler}
	// 		return srv.Serve(l)
	// }
	/*
		func (srv *Server) Serve(l net.Listener) error {
			defer l.Close()
			var tempDelay time.Duration // how long to sleep on accept failure
			for {
				rw, e := l.Accept()        // (c Conn, err error) 返回的是一个 Conn对象
				if e != nil {
					if ne, ok := e.(net.Error); ok && ne.Temporary() {
						if tempDelay == 0 {
							tempDelay = 5 * time.Millisecond
						} else {
							tempDelay *= 2
						}
						if max := 1 * time.Second; tempDelay > max {
							tempDelay = max
						}
						log.Printf("http: Accept error: %v; retrying in %v", e, tempDelay)
						time.Sleep(tempDelay)
						continue
					}
					return e
				}
				tempDelay = 0
				if srv.ReadTimeout != 0 {
					rw.SetReadDeadline(time.Now().Add(srv.ReadTimeout))
				}
				if srv.WriteTimeout != 0 {
					rw.SetWriteDeadline(time.Now().Add(srv.WriteTimeout))
				}
				// // A conn represents the server side of an HTTP connection.
				// func (srv *Server) newConn(rwc net.Conn) (c *conn, err error)
				c, err := srv.newConn(rw)
				if err != nil {
					continue
				}
				// // Serve a new connection.
				go c.serve()
			}
			panic("not reached")
		}
	*/
	// 在 Serve 中完成了如下工作：
	// 启动一个for循环，在循环体中监听是否Accept请求
	// 如果监听到请求通过了，实例化一个Conn，并且开启一个goroutine为这个请求进行服务go c.serve()
	// 在 conn 的 serve 里面，读取每个请求的内容w, err := c.readRequest()
	// 判断c.server.Handler是否为空，如果没有设置handler（我们这里使用的是web.go 的 Server），handler就设置为DefaultServeMux
	// 调用handler的ServeHttp，这里即调用 func (s *Server) ServeHTTP(c http.ResponseWriter, req *http.Request)
	// 根据request选择handler，并且进入到这个handler的ServeHTTP
	// 判断是否有路由能满足这个request（循环遍历ServerMux的muxEntry）的 handler
	err = http.Serve(s.l, mux)
	// TODO:为啥还要 Close 一边，在 srv.Serve(l) 里面已经有一个 defer l.Close() 了
	s.l.Close()
}

// RunFcgi starts the web application and serves FastCGI requests for s.
func (s *Server) RunFcgi(addr string) {
	s.initServer()
	s.Logger.Printf("web.go serving fcgi %s\n", addr)
	s.listenAndServeFcgi(addr)
}

// RunScgi starts the web application and serves SCGI requests for s.
func (s *Server) RunScgi(addr string) {
	s.initServer()
	s.Logger.Printf("web.go serving scgi %s\n", addr)
	s.listenAndServeScgi(addr)
}

// RunTLS starts the web application and serves HTTPS requests for s.
// 运行服务器，响应 HTTPS 的请求
func (s *Server) RunTLS(addr string, config *tls.Config) error {
	s.initServer()
	mux := http.NewServeMux()
	mux.Handle("/", s)
	// 监听 addr 地址的链接状况，config 必须不能为 nil,而且必须至少有一个 certificate
	// 在 HTTP 的请求方式下，l, err := net.Listen("tcp", addr)
	l, err := tls.Listen("tcp", addr, config)
	if err != nil {
		log.Fatal("Listen:", err)
		return err
	}

	s.l = l
	return http.Serve(s.l, mux)
}

// Close stops server s.
// 关闭服务
func (s *Server) Close() {
	if s.l != nil {
		s.l.Close()
	}
}

// safelyCall invokes `function` in recover block
func (s *Server) safelyCall(function reflect.Value, args []reflect.Value) (resp []reflect.Value, e interface{}) {
	// Go 没有 try ... catch ... finally 这种结构化异常处理,⽽是⽤ panic 代替 throw/raise 引发错误,然
	// 后在 defer 中⽤用 recover 函数捕获错误。
	// 如果不使⽤ recover 捕获,则 panic 沿着 "调⽤堆栈 (call stack)" 向外层传递。
	// recover 仅在 defer 函数中使⽤才会终⽌错误,此时函数执⾏流程已经中断,⽆无法像 catch 那样恢复到后续位置继续执⾏。
	defer func() {
		if err := recover(); err != nil {
			// 如果我们在配置文件中定义了 RecoverPanic 为false 的话，那么就直接抛出异常
			if !s.Config.RecoverPanic {
				// go back to panic
				panic(err)
			} else {
				e = err
				resp = nil
				s.Logger.Println("Handler crashed with error", err)
				for i := 1; ; i += 1 {
					// 获取当前的调用栈信息
					_, file, line, ok := runtime.Caller(i)
					if !ok {
						break
					}
					s.Logger.Println(file, line)
				}
			}
		}
	}()
	return function.Call(args), nil
}

// requiresContext determines whether 'handlerType' contains
// an argument to 'web.Ctx' as its first argument
// 检测处理函数第一个参数是否是 web.Ctx 类型，如果是web.Ctx的话，那么返回true
// 否则返回 false
func requiresContext(handlerType reflect.Type) bool {
	//if the method doesn't take arguments, no
	// 如果没有输入参数
	if handlerType.NumIn() == 0 {
		return false
	}

	//if the first argument is not a pointer, no
	// 检测传入的第一个参数是否为指针
	a0 := handlerType.In(0)
	if a0.Kind() != reflect.Ptr {
		return false
	}
	//if the first argument is a context, yes
	if a0.Elem() == contextType {
		return true
	}

	return false
}

// tryServingFile attempts to serve a static file, and returns
// whether or not the operation is successful.
// It checks the following directories for the file, in order:
// 1) Config.StaticDir
// 2) The 'static' directory in the parent directory of the executable.
// 3) The 'static' directory in the current working directory
// 检查是否是有静态文件，如果是静态文件返回 true,否则返回 false
// 首先会检查是否自定义了静态文件夹，如果没有就去当前执行程序的父文件夹和当前工作目录下是否有 static 文件夹
// 如果检查到了该文件在里面，就调用静态文件服务，并且返回true.
func (s *Server) tryServingFile(name string, req *http.Request, w http.ResponseWriter) bool {
	//try to serve a static file
	// 检测我们是否在 Config 重新设置了 static 的文件夹路径
	if s.Config.StaticDir != "" {
		staticFile := path.Join(s.Config.StaticDir, name)
		if fileExists(staticFile) {
			http.ServeFile(w, req, staticFile)
			return true
		}
	} else {
		for _, staticDir := range defaultStaticDirs {
			staticFile := path.Join(staticDir, name)
			if fileExists(staticFile) {
				http.ServeFile(w, req, staticFile)
				return true
			}
		}
	}
	return false
}

// the main route handler in web.go
func (s *Server) routeHandler(req *http.Request, w http.ResponseWriter) {
	requestPath := req.URL.Path
	// web.go 中定义了 Context 结构体
	ctx := Context{req, map[string]string{}, s, w}

	//log the request
	var logEntry bytes.Buffer
	// 设置文本终端的显示样式和显示内容
	// “\033”引导非常规字符序列
	// “m”意味着设置属性然后结束非常规字符序列
	// 32 设置绿色前景
	// 1 设置粗体
	// 0 重新设置属性到缺省设置
	// 31 设置红色前景
	fmt.Fprintf(&logEntry, "\033[31;1m%s %s\033[0m", req.Method, requestPath)

	// ignore errors from ParseForm because it's usually harmless.
	// 解析HTTP请求的参数，包括URL中query-string、POST的数据、PUT的数据
	// 会将解析的数据保存到 req.Form 里面，
	// 可以通过 req.Form["name"] 或者 req.FormValue("name")，来获得特定参数的值
	req.ParseForm()
	// 将解析后得到的 req.Form 数据保存到 Context 的 Params 里
	if len(req.Form) > 0 {
		for k, v := range req.Form {
			ctx.Params[k] = v[0]
		}
		fmt.Fprintf(&logEntry, "\n\033[37;1mParams: %v\033[0m\n", ctx.Params)
	}
	ctx.Server.Logger.Print(logEntry.String())

	//set some default headers
	// 设置一些响应的头信息
	ctx.SetHeader("Server", "web.go", true)
	tm := time.Now().UTC()
	// webTime 返回的是以 GMT 结尾的时间格式
	ctx.SetHeader("Date", webTime(tm), true)

	// 如果请求方法是 GET 或者 HEAD,先去检测是否请求的是静态文件，如果是就直接启用静态文件服务，并且返回
	// 如果没有检测到相应的静态文件，那么继续
	if req.Method == "GET" || req.Method == "HEAD" {
		if s.tryServingFile(requestPath, req, w) {
			return
		}
	}

	//Set the default content-type
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8", true)

	for i := 0; i < len(s.routes); i++ {
		route := s.routes[i]
		cr := route.cr
		//if the methods don't match, skip this handler (except HEAD can be used in place of GET)
		// 请求方法如果不匹配就直接跳过本次循环
		if req.Method != route.method && !(req.Method == "HEAD" && route.method == "GET") {
			continue
		}

		// 如果请求的地址不匹配，那么就直接跳过本次循环
		if !cr.MatchString(requestPath) {
			continue
		}
		// 查找匹配的地址，这里指的是去查找第一个匹配的地址，包括子匹配项。如下：
		// r, _ := regexp.Compile("p([a-z]+)ch")
		// fmt.Println(r.FindStringSubmatch("peach punch"))   //[peach ea]
		// 在 peach 和 punch中第一个和正则表达式匹配的字符串，还有匹配其子表达式的部分
		match := cr.FindStringSubmatch(requestPath)

		// 如果和我们的请求地址长度不等，直接跳过本次循环
		if len(match[0]) != len(requestPath) {
			continue
		}

		var args []reflect.Value
		handlerType := route.handler.Type()
		// 如果我们的处理函数第一个参数是 web.Ctx 类型的话，将其加入到参数集里
		if requiresContext(handlerType) {
			args = append(args, reflect.ValueOf(&ctx))
		}
		// TODO:
		for _, arg := range match[1:] {
			args = append(args, reflect.ValueOf(arg))
		}

		// 将参数传递给处理函数，并调用处理函数，在这里对异常进行了处理
		ret, err := s.safelyCall(route.handler, args)
		if err != nil {
			//there was an error or panic while calling the handler
			// 如果抛出了异常，则显示错误
			ctx.Abort(500, "Server Error")
		}

		// 如果处理函数没有返回值，直接跳过本次循环
		if len(ret) == 0 {
			return
		}

		sval := ret[0]

		var content []byte

		if sval.Kind() == reflect.String {
			content = []byte(sval.String())
		} else if sval.Kind() == reflect.Slice && sval.Type().Elem().Kind() == reflect.Uint8 {
			content = sval.Interface().([]byte)
		}
		// Itoa 是 FormatInt(i, 10)
		// 计算返回值的长度，然后将长度信息传递给响应头
		ctx.SetHeader("Content-Length", strconv.Itoa(len(content)), true)
		_, err = ctx.ResponseWriter.Write(content)
		if err != nil {
			ctx.Server.Logger.Println("Error during write: ", err)
		}
		return
	}

	// try serving index.html or index.htm
	// 如果没有找到匹配的路由，那么就去调用静态路径下的 index.html 或者 index.htm 页面
	if req.Method == "GET" || req.Method == "HEAD" {
		if s.tryServingFile(path.Join(requestPath, "index.html"), req, w) {
			return
		} else if s.tryServingFile(path.Join(requestPath, "index.htm"), req, w) {
			return
		}
	}
	// 如果 index.html 或者 index.htm 静态文件都没有找到的话，那么就返回 404 错误
	ctx.Abort(404, "Page not found")
}

// SetLogger sets the logger for server s
func (s *Server) SetLogger(logger *log.Logger) {
	s.Logger = logger
}
