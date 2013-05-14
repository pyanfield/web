// Package web is a lightweight web framework for Go. It's ideal for
// writing simple, performant backend web services.

// 注意在 Go 中的初始化过程，最先调用的是 import 的包，如果包里面还有其它的 import 包，那么继续递归调用。
// 直到最底层，在最底层中先初始化 const 变量，然后是 var 变量，再然后是 init 函数。
// 在这个项目中，作者没有按照前面是变量，后面是方法的方式组织代码，所以项目最开始的部分是先初始化下列变量：
/* 
   var contextType reflect.Type
   var defaultStaticDirs []string

   // Config is the configuration of the main server.
   // 声明一个 Config 变量，这个变量是所有的配置信息, 参见 server.go 文件中的 ServerConfig 结构体
   var Config = &ServerConfig{
       RecoverPanic: true,
   }

   // 声明一个 mainServer 变量，参见 server.go 
   var mainServer = NewServer()
*/
// 初始化上述变量之后，就是要执行 init() 函数做包的初始化

package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// A Context object is created for every incoming HTTP request, and is
// passed to handlers as an optional first argument. It provides information
// about the request, including the http.Request object, the GET and POST params,
// and acts as a Writer for the response.
// 每一个 HTTP 的请求都会创建一个 Context 对象，它可以作为处理函数的第一个可选参数
type Context struct {
	Request *http.Request     // HTTP 请求
	Params  map[string]string // 参数列表
	Server  *Server           // Server
	// 这个接口主要用于 HTTP 处理函数去构造 HTTP 的响应
	http.ResponseWriter
}

// WriteString writes string data into the response object.
func (ctx *Context) WriteString(content string) {
	ctx.ResponseWriter.Write([]byte(content))
}

// Abort is a helper method that sends an HTTP header and an optional
// body. It is useful for returning 4xx or 5xx errors.
// Once it has been called, any return value from the handler will
// not be written to the response.
// 返回错误信息，主要是针对 4xx 和 5xx 错误码的处理。
func (ctx *Context) Abort(status int, body string) {
	ctx.ResponseWriter.WriteHeader(status)
	ctx.ResponseWriter.Write([]byte(body))
}

// Redirect is a helper method for 3xx redirects.
func (ctx *Context) Redirect(status int, url_ string) {
	ctx.ResponseWriter.Header().Set("Location", url_)
	ctx.ResponseWriter.WriteHeader(status)
	ctx.ResponseWriter.Write([]byte("Redirecting to: " + url_))
}

// Notmodified writes a 304 HTTP response
func (ctx *Context) NotModified() {
	ctx.ResponseWriter.WriteHeader(304)
}

// NotFound writes a 404 HTTP response
func (ctx *Context) NotFound(message string) {
	ctx.ResponseWriter.WriteHeader(404)
	ctx.ResponseWriter.Write([]byte(message))
}

// ContentType sets the Content-Type header for an HTTP response.
// For example, ctx.ContentType("json") sets the content-type to "application/json"
// If the supplied value contains a slash (/) it is set as the Content-Type
// verbatim. The return value is the content type as it was
// set, or an empty string if none was found.
func (ctx *Context) ContentType(val string) string {
	var ctype string
	if strings.ContainsRune(val, '/') {
		ctype = val
	} else {
		if !strings.HasPrefix(val, ".") {
			val = "." + val
		}
		ctype = mime.TypeByExtension(val)
	}
	if ctype != "" {
		ctx.Header().Set("Content-Type", ctype)
	}
	return ctype
}

// SetHeader sets a response header. If `unique` is true, the current value
// of that header will be overwritten . If false, it will be appended.
// 设置响应头， 如果 unique 为 true 的话，那么当前的值将会被覆盖
// 如果为 false ，那么将添加现在的值到当前的值之后
func (ctx *Context) SetHeader(hdr string, val string, unique bool) {
	if unique {
		ctx.Header().Set(hdr, val)
	} else {
		ctx.Header().Add(hdr, val)
	}
}

// SetCookie adds a cookie header to the response.
func (ctx *Context) SetCookie(cookie *http.Cookie) {
	ctx.SetHeader("Set-Cookie", cookie.String(), false)
}

func getCookieSig(key string, val []byte, timestamp string) string {
	hm := hmac.New(sha1.New, []byte(key))

	hm.Write(val)
	hm.Write([]byte(timestamp))

	hex := fmt.Sprintf("%02x", hm.Sum(nil))
	return hex
}

func (ctx *Context) SetSecureCookie(name string, val string, age int64) {
	//base64 encode the val
	if len(ctx.Server.Config.CookieSecret) == 0 {
		ctx.Server.Logger.Println("Secret Key for secure cookies has not been set. Please assign a cookie secret to web.Config.CookieSecret.")
		return
	}
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	encoder.Write([]byte(val))
	encoder.Close()
	vs := buf.String()
	vb := buf.Bytes()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := getCookieSig(ctx.Server.Config.CookieSecret, vb, timestamp)
	cookie := strings.Join([]string{vs, timestamp, sig}, "|")
	ctx.SetCookie(NewCookie(name, cookie, age))
}

func (ctx *Context) GetSecureCookie(name string) (string, bool) {
	for _, cookie := range ctx.Request.Cookies() {
		if cookie.Name != name {
			continue
		}

		parts := strings.SplitN(cookie.Value, "|", 3)

		val := parts[0]
		timestamp := parts[1]
		sig := parts[2]

		if getCookieSig(ctx.Server.Config.CookieSecret, []byte(val), timestamp) != sig {
			return "", false
		}

		ts, _ := strconv.ParseInt(timestamp, 0, 64)

		if time.Now().Unix()-31*86400 > ts {
			return "", false
		}

		buf := bytes.NewBufferString(val)
		encoder := base64.NewDecoder(base64.StdEncoding, buf)

		res, _ := ioutil.ReadAll(encoder)
		return string(res), true
	}
	return "", false
}

// small optimization: cache the context type instead of repeteadly calling reflect.Typeof
var contextType reflect.Type

// 用于存储静态文件路径， webgo 会检索执行程序路径和程序路径下的 static 文件夹
var defaultStaticDirs []string

// 为 contextType 和 defaultStaticDirs 赋初始值
func init() {
	// 返回 Context{} 的类型 web.Context 赋值给 contenxtType
	contextType = reflect.TypeOf(Context{})
	//find the location of the exe file
	// 返回当前应用程序的路径，比如 ~/Workspace/golang/src/web.go/hello_world
	wd, _ := os.Getwd()
	// os.Args 返回的是命令行的参数信息，而第一个数据就是这个程序执行文件的路径及名字
	// 使用 'go run' 命令的时候，会自动生成一个 a.out 的文件
	// 所以如果用 'go run' 命令来编译 hello_world 程序的话，会生成一个 a.out 文件
	// 下面的 arg0 将会是经过处理过的干净的 a.out 的路径。
	arg0 := path.Clean(os.Args[0])
	var exeFile string
	// 如果程序的名字是以 '/' 开头的路径，那么就让 exeFile 等于这个值，否则的话，否则就将程序路径添加到当前的应用程序路径下
	if strings.HasPrefix(arg0, "/") {
		exeFile = arg0
	} else {
		//TODO for robustness, search each directory in $PATH
		exeFile = path.Join(wd, arg0)
	}
	parent, _ := path.Split(exeFile)
	// 添加 static 文件路径到执行文件路径和程序路径，因为 webgo 在检索静态文件的时候会检索这两个位置
	defaultStaticDirs = append(defaultStaticDirs, path.Join(parent, "static"))
	defaultStaticDirs = append(defaultStaticDirs, path.Join(wd, "static"))
	return
}

// Process invokes the main server's routing system.
func Process(c http.ResponseWriter, req *http.Request) {
	mainServer.Process(c, req)
}

// Run starts the web application and serves HTTP requests for the main server.
// 运行服务器  web.Run("0.0.0.0:9999")
func Run(addr string) {
	mainServer.Run(addr)
}

// RunTLS starts the web application and serves HTTPS requests for the main server.
func RunTLS(addr string, config *tls.Config) {
	mainServer.RunTLS(addr, config)
}

// RunScgi starts the web application and serves SCGI requests for the main server.
func RunScgi(addr string) {
	mainServer.RunScgi(addr)
}

// RunFcgi starts the web application and serves FastCGI requests for the main server.
func RunFcgi(addr string) {
	mainServer.RunFcgi(addr)
}

// Close stops the main server.
func Close() {
	mainServer.Close()
}

// Get adds a handler for the 'GET' http method in the main server.
// 为 HTTP GET 方法添加一个处理程序，这里封装了 miniServer 的 Get 方法，参见 server.go 中的 Get 方法
// web.Get("/(.*)", hello)
func Get(route string, handler interface{}) {
	mainServer.Get(route, handler)
}

// Post adds a handler for the 'POST' http method in the main server.
func Post(route string, handler interface{}) {
	// TODO:如果参考 Get 的实现方法，那么这个地方应该是用 mainServer.Post(route, handler)
	// 不过在 mainServer.Post 中也是封装了 mainServer.addRoute 方法。
	mainServer.addRoute(route, "POST", handler)
}

// Put adds a handler for the 'PUT' http method in the main server.
func Put(route string, handler interface{}) {
	mainServer.addRoute(route, "PUT", handler)
}

// Delete adds a handler for the 'DELETE' http method in the main server.
func Delete(route string, handler interface{}) {
	mainServer.addRoute(route, "DELETE", handler)
}

// Match adds a handler for an arbitrary http method in the main server.
func Match(method string, route string, handler interface{}) {
	mainServer.addRoute(route, method, handler)
}

// SetLogger sets the logger for the main server.
func SetLogger(logger *log.Logger) {
	mainServer.Logger = logger
}

// Config is the configuration of the main server.
// 声明一个 Config 变量，这个变量是所有的配置信息, 参见 server.go 文件中的 ServerConfig 结构体
var Config = &ServerConfig{
	RecoverPanic: true,
}

// 声明一个 mainServer 变量， 参见 server.go 文件
var mainServer = NewServer()
