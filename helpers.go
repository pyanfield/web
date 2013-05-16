package web

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// internal utility methods
// 返回 RFC1123 格式的时间
func webTime(t time.Time) string {
	// RFC1123 :  "Mon, 02 Jan 2006 15:04:05 MST"
	ftime := t.Format(time.RFC1123)
	// 检测后缀是都有 "UTC"，如果有就替换成 "GMT"
	// GMT 是格林威治时间，UTC 表示的是世界协调时间，比GMT更加精确严谨
	if strings.HasSuffix(ftime, "UTC") {
		ftime = ftime[0:len(ftime)-3] + "GMT"
	}
	return ftime
}

// 判断文件夹是否存在
func dirExists(dir string) bool {
	// os.Stat 返回(fi FileInfo, err error)
	d, e := os.Stat(dir)
	switch {
	case e != nil:
		return false
	case !d.IsDir():
		return false
	}

	return true
}

// 检测文件是否存在
func fileExists(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}

	return !info.IsDir()
}

// Urlencode is a helper method that converts a map into URL-encoded form data.
// It is a useful when constructing HTTP POST requests.
// 对链接地址进行 encode 编码
func Urlencode(data map[string]string) string {
	var buf bytes.Buffer
	for k, v := range data {
		buf.WriteString(url.QueryEscape(k))
		buf.WriteByte('=')
		buf.WriteString(url.QueryEscape(v))
		buf.WriteByte('&')
	}
	s := buf.String()
	return s[0 : len(s)-1]
}

var slugRegex = regexp.MustCompile(`(?i:[^a-z0-9\-_])`)

// Slug is a helper function that returns the URL slug for string s.
// It's used to return clean, URL-friendly strings that can be
// used in routing.
func Slug(s string, sep string) string {
	if s == "" {
		return ""
	}
	slug := slugRegex.ReplaceAllString(s, sep)
	if slug == "" {
		return ""
	}
	quoted := regexp.QuoteMeta(sep)
	sepRegex := regexp.MustCompile("(" + quoted + "){2,}")
	slug = sepRegex.ReplaceAllString(slug, sep)
	sepEnds := regexp.MustCompile("^" + quoted + "|" + quoted + "$")
	slug = sepEnds.ReplaceAllString(slug, "")
	return strings.ToLower(slug)
}

// NewCookie is a helper method that returns a new http.Cookie object.
// Duration is specified in seconds. If the duration is zero, the cookie is permanent.
// This can be used in conjunction with ctx.SetCookie.
// 创建新的 cookie，返回 http.Cookie 对象。 其中的 age 为cookie 的有效期，以秒计，如果 age 为 0
// 那么这个 cookie 是永久有效。 NewCookie 可以和 ctx.SetCookie 一起使用
func NewCookie(name string, value string, age int64) *http.Cookie {
	var utctime time.Time
	if age == 0 {
		// 2^31 - 1 seconds (roughly 2038)
		utctime = time.Unix(2147483647, 0)
	} else {
		utctime = time.Unix(time.Now().Unix()+age, 0)
	}
	return &http.Cookie{Name: name, Value: value, Expires: utctime}
}