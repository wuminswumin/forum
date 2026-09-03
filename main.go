// Gin 版：main.go 只当"入口"——负责启动流程
// 分工不变：
//   main.go      → 程序入口：启动数据库 + 注册路由 + 开门营业
//   db.go        → 数据库：连接、建表（用 GORM）
//   handlers.go  → 接口处理：接待客人（用 Gin）
//   auth.go      → 认证：注册、登录、门卫（JWT）
//   response.go  → 统一响应信封：所有回复的固定格式

package main

import (
	"fmt"

	"github.com/gin-gonic/gin" // Gin 框架
)

func main() {
	// 第 1 步：打开数据库
	initDB()

	// 第 2 步：创建 Gin 引擎（相当于开餐厅前先装好"服务台"）
	// gin.Default() 自带两件好东西：请求日志 + 程序崩溃时自动恢复不挂掉
	r := gin.Default()

	// 第 3 步：注册路由
	// Gin 的写法更口语化：r.GET 就是"注册一个 GET 接口"

	// 欢迎接口：不用登录，看一眼服务活着没有
	r.GET("/api/hello", func(c *gin.Context) {
		ok(c, gin.H{"message": "你好！欢迎来到吾民的论坛后端 🎉"})
	})

	// ========== 用户与鉴权（不用登录） ==========
	r.POST("/api/v1/auth/register", register) // 注册
	r.POST("/api/v1/auth/login", login)       // 登录（拿到 token 才能用下面的接口）

	// ========== 帖子（全部要登录：中间夹了 authMiddleware 门卫，先验登录再干活） ==========
	r.GET("/api/v1/posts", authMiddleware, listPosts)                       // 帖子列表（分页）
	r.POST("/api/v1/posts", authMiddleware, createPost)                     // 发帖
	r.POST("/api/v1/posts/likes", authMiddleware, batchLikeStatus)          // 批量查点赞状态
	r.GET("/api/v1/posts/:post_id", authMiddleware, getPost)                // 帖子详情（带评论）
	r.DELETE("/api/v1/posts/:post_id", authMiddleware, deletePost)          // 删自己的帖
	r.POST("/api/v1/posts/:post_id/comment", authMiddleware, createComment) // 发评论
	r.POST("/api/v1/posts/:post_id/like", authMiddleware, toggleLike)       // 点赞/取消点赞

	// ========== 管理员（要登录 + 要 admin 角色，角色检查在处理函数里做） ==========
	r.DELETE("/api/v1/admin/posts/:post_id", authMiddleware, adminDeletePost) // 管理员删任意帖

	// 第 4 步：开门营业（8080 号门）
	fmt.Println("服务器已启动: http://localhost:8080")
	r.Run(":8080")
}
