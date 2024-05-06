package ginmodule

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/gin-gonic/gin"
	"time"
)

// Logger 是一个自定义的日志中间件
func Logger() gin.HandlerFunc {

	return func(c *gin.Context) {
		// 处理请求前记录日志
		// 开始时间
		startTime := time.Now()
		// 调用该请求的剩余处理程序
		c.Next()
		// 结束时间
		endTime := time.Now()
		// 执行时间
		latencyTime := endTime.Sub(startTime)

		// 请求IP
		clientIP := c.ClientIP()
		// remoteIP := c.RemoteIP()

		// 请求方式
		reqMethod := c.Request.Method
		// 请求路由
		reqUri := c.Request.RequestURI
		// 请求协议
		reqProto := c.Request.Proto
		// 请求来源
		repReferer := c.Request.Referer()
		// 请求UA
		reqUA := c.Request.UserAgent()

		// 请求响应内容长度
		resLength := c.Writer.Size()
		if resLength < 0 {
			resLength = 0
		}
		// 响应状态码
		statusCode := c.Writer.Status()

		log.SDebug(fmt.Sprintf(
			"%s | %3d | %s %10s | \033[44;37m%-6s\033[0m %s %s  | %10v | \"%s\" \"%s\"",
			colorForStatus(statusCode),
			statusCode,
			colorForStatus(0),
			clientIP,
			// remoteIP,
			reqMethod,
			reqUri,
			reqProto,
			latencyTime,
			reqUA,
			repReferer,
		))

	}
}

// colorForStatus 根据 HTTP 状态码返回 ANSI 颜色代码
func colorForStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "\033[42;1;37m" // green
	case code >= 300 && code < 400:
		return "\033[34m" // blue
	case code >= 400 && code < 500:
		return "\033[33m" // yellow
	case code == 0:
		return "\033[0m" // cancel
	default:
		return "\033[31m" // red
	}
}
