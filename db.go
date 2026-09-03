// GORM 版：数据库相关代码
// GORM 是"翻译官"：你写 Go 语言，它翻译成 SQL 跟数据库交流

package main

import (
	"fmt"
	"log"

	"github.com/glebarez/sqlite" // 纯 Go 版 SQLite 驱动（Windows 上不用装任何东西）
	"gorm.io/gorm"               // GORM 框架本体
)

// db 是全局数据库连接：所有接口共用这一个
var db *gorm.DB

// Post 帖子结构体（论坛版：留言升级为"帖子"，是论坛的主角）
// 结构体 = 表的"设计图"：定义了什么字段，表就有什么列
type Post struct {
	ID        uint   `gorm:"primaryKey" json:"id"`                  // 编号：主键（GORM 自动自增）
	UserID    uint   `gorm:"not null" json:"-"`                     // 作者编号（内部关联用，输出时换成下面的 Author 对象）
	Content   string `gorm:"not null" json:"content"`               // 内容：不允许为空
	CreatedAt string `json:"created_at"`                            // 发布时间（RFC3339 标准格式，如 2026-08-29T10:00:00+08:00）
	Author    User   `gorm:"foreignKey:UserID" json:"author"`       // 作者：belongsTo 关联（一条帖子属于一个用户），查询时 Preload 填充
	LikeCount    int64 `gorm:"-" json:"like_count"`                 // 点赞数：不在表里存，查询时现算（gorm:"-" = 不是数据库列）
	CommentCount int64 `gorm:"-" json:"comment_count"`              // 评论数：同理，查询时现算
}

// User 用户结构体：用户表的设计图
type User struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"uniqueIndex;not null" json:"username"` // 学号（账号）：数据库层面保证不重复
	Name         string `gorm:"not null;default:''" json:"name"`      // 姓名
	PasswordHash string `gorm:"not null" json:"-"`                    // 密码哈希（json:"-" = 转 JSON 时藏起来，绝不外传！）
	Role         string `gorm:"not null;default:student" json:"role"` // 角色：student=学生 admin=管理员
	CreatedAt    string `json:"-"`                                    // 注册时间（验收标准里用户信息只认 id/username/name/role 四件套，所以对外藏起来）
}

// Comment 评论结构体：一条帖子下面可以挂很多评论（一对多）
type Comment struct {
	ID        uint   `gorm:"primaryKey" json:"id"`            // 评论编号
	PostID    uint   `gorm:"index;not null" json:"post_id"`   // 挂在哪个帖子下（加索引：按帖子查评论快）
	UserID    uint   `gorm:"not null" json:"-"`               // 谁评论的（内部关联用）
	Content   string `gorm:"not null" json:"content"`         // 评论内容
	CreatedAt string `json:"created_at"`                      // 评论时间
	Author    User   `gorm:"foreignKey:UserID" json:"author"` // 评论者：belongsTo 关联
}

// Like 点赞记录结构体：谁给哪个帖子点过赞
// uniqueIndex 联合唯一索引 = 数据库层面保证"同一个人对同一篇帖子只能有一条记录"，
// 就算代码有 bug 想重复点赞也插不进去——这是最可靠的防重复手段
type Like struct {
	ID     uint `gorm:"primaryKey" json:"-"`                             // 编号（点赞记录本身不需要对外输出）
	UserID uint `gorm:"uniqueIndex:idx_user_post;not null" json:"-"`     // 点赞的人
	PostID uint `gorm:"uniqueIndex:idx_user_post;not null" json:"-"`     // 被赞的帖子
}

// initDB 启动时调用：连接数据库 + 自动建表
func initDB() {
	var err error
	// 连接数据库文件 forum.db（不存在会自动创建）
	db, err = gorm.Open(sqlite.Open("forum.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("打开数据库失败:", err)
	}

	// AutoMigrate = 自动建表！
	// 拿结构体对照表：表不存在就建；字段少了就补。
	// 现在维护四张表：用户 + 帖子 + 评论 + 点赞
	err = db.AutoMigrate(&User{}, &Post{}, &Comment{}, &Like{})
	if err != nil {
		log.Fatal("建表失败:", err)
	}
	fmt.Println("数据库就绪: forum.db")
}
