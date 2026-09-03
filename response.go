// 统一响应信封：全站所有接口的回复都套同一个格式（任务验收标准要求的）
//   成功：{"code": 0, "msg": "success", "data": <实际数据>}
//   失败：{"code": <HTTP状态码>, "msg": "<错误说明>", "data": null}
// 类比：寄快递必须用统一规格的信封，收件人（测试工具）才认得

package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ok 回复成功信封（HTTP 200）：查询类接口用
// data 是信封里装的东西：可以是对象、数组，也可以传 nil（表示没东西可装，输出 "data": null）
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "success", "data": data})
}

// created 回复成功信封（HTTP 201）：创建类接口用（注册、发帖、发评论）
// 201 的意思是"资源创建成功"，比 200 多了一层语义
func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"code": 0, "msg": "success", "data": data})
}

// fail 回复失败信封：code 和 HTTP 状态码保持一致（测试工具会同时核对这两处）
// 注意 data 永远输出 null，而不是空对象 {}
func fail(c *gin.Context, httpStatus int, msg string) {
	c.JSON(httpStatus, gin.H{"code": httpStatus, "msg": msg, "data": nil})
}
