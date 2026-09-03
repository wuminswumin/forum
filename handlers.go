// 接口处理函数：帖子的查看、发布、删除，以及评论、点赞
// 本文件负责"接待客人"——客人点哪个接口，就由哪个函数伺候
// 所有回复都走 response.go 里的统一信封（ok / created / fail）

package main

import (
	"net/http"
	"strconv" // 字符串和数字互相转换
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ========== 小工具函数 ==========

// parsePostID 把路径参数 post_id 从字符串转成数字；转不出来（非数字）返回 0
// 调用方判断返回 0 就当"帖子不存在"处理（404）
func parsePostID(c *gin.Context) uint {
	id, err := strconv.ParseUint(c.Param("post_id"), 10, 64)
	if err != nil {
		return 0
	}
	return uint(id)
}

// fillAuthors 给一批帖子填上作者信息（一次查询所有涉及的用户，避免一条一条查——N+1 问题）
// 帖子表里只存了作者的编号（UserID），对外输出时要换成完整的用户对象
func fillAuthors(posts []Post) {
	// 1. 收集所有帖子涉及的用户编号
	ids := make([]uint, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.UserID)
	}
	// 2. 一条 SQL 把用户全查出来，装进"编号 → 用户"的对照表
	var users []User
	db.Where("id IN ?", ids).Find(&users)
	userMap := make(map[uint]User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	// 3. 按编号把作者填回每一条帖子
	for i := range posts {
		posts[i].Author = userMap[posts[i].UserID]
	}
}

// countRow 是聚合查询的"小篮子"：装"哪个帖子 + 有多少条"
type countRow struct {
	PostID uint  `gorm:"column:post_id"`
	N      int64 `gorm:"column:n"`
}

// fillCounts 给一批帖子填上点赞数和评论数
// 同样用聚合查询（GROUP BY）一次算完所有帖子，而不是每条帖子数两次——省查询、省时间
func fillCounts(posts []Post) {
	ids := make([]uint, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
	}

	// 点赞数：SELECT post_id, COUNT(*) FROM likes WHERE post_id IN (...) GROUP BY post_id
	var likeRows []countRow
	db.Model(&Like{}).Select("post_id, COUNT(*) AS n").
		Where("post_id IN ?", ids).Group("post_id").Scan(&likeRows)

	// 评论数：同理
	var commentRows []countRow
	db.Model(&Comment{}).Select("post_id, COUNT(*) AS n").
		Where("post_id IN ?", ids).Group("post_id").Scan(&commentRows)

	// 把两个对照表叠到一起，按帖子编号填回去
	counts := make(map[uint][2]int64, len(ids))
	for _, r := range likeRows {
		c := counts[r.PostID]
		c[0] = r.N
		counts[r.PostID] = c
	}
	for _, r := range commentRows {
		c := counts[r.PostID]
		c[1] = r.N
		counts[r.PostID] = c
	}
	for i := range posts {
		posts[i].LikeCount = counts[posts[i].ID][0]
		posts[i].CommentCount = counts[posts[i].ID][1]
	}
}

// cascadeDeletePost 删除帖子，并把它的评论和点赞一并清掉（防止留下"孤儿数据"）
// 用"事务"保证三步要么全成功、要么全回滚——不会出现帖子删了评论还挂着的半吊子状态
func cascadeDeletePost(postID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&Post{}, postID).Error; err != nil { // 删帖子本身
			return err
		}
		if err := tx.Where("post_id = ?", postID).Delete(&Comment{}).Error; err != nil { // 删它的评论
			return err
		}
		if err := tx.Where("post_id = ?", postID).Delete(&Like{}).Error; err != nil { // 删它的点赞
			return err
		}
		return nil // 三步全成，事务提交
	})
}

// ========== 发帖（要登录）：POST /api/v1/posts ==========
func createPost(c *gin.Context) {
	// 0. 从上下文取当前登录用户（authMiddleware 门卫放进来的）
	//    能走到这里 = 已经通过登录验证
	uid := c.GetUint("uid")

	// 1. 解析请求体（客户端只需传 content，作者由服务器从 token 里取！）
	var input struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 2. 内容校验：1 ~ 2000 字
	input.Content = strings.TrimSpace(input.Content)
	if len(input.Content) < 1 || len(input.Content) > 2000 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 3. 组装帖子：作者编号从 token 来，不信任客户端传来的任何"我是谁"
	post := Post{
		UserID:    uid,
		Content:   input.Content,
		CreatedAt: time.Now().Format(time.RFC3339), // 标准时间格式，如 2026-08-29T15:04:05+08:00
	}
	// Omit("Author") = 保存时跳过 Author 字段，否则 GORM 会把作者也当新用户插进表里！
	if err := db.Omit("Author").Create(&post).Error; err != nil {
		fail(c, http.StatusInternalServerError, "发布失败")
		return
	}

	// 4. 把作者信息查出来填上（对外要输出完整的用户对象）
	db.First(&post.Author, post.UserID)

	// 5. 回复 201 + 成功信封
	created(c, post)
}

// ========== 看帖子列表（分页版）：GET /api/v1/posts?page=1&page_size=20 ==========
func listPosts(c *gin.Context) {
	// 1. 读取分页参数
	//    Query 参数 = URL 问号后面的东西：/api/v1/posts?page=2&page_size=20
	//    DefaultQuery("page", "1") 读法：读 page 参数；用户没填就默认 "1"
	pageStr := c.DefaultQuery("page", "1")
	sizeStr := c.DefaultQuery("page_size", "20")

	// 2. 字符串转数字 + 范围校验（page 从 1 起；每页 1~100 条，和验收标准一致）
	page, err1 := strconv.Atoi(pageStr) // Atoi = 文字转整数
	size, err2 := strconv.Atoi(sizeStr)
	if err1 != nil || err2 != nil || page < 1 || size < 1 || size > 100 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 3. 先数总数：给前端算"一共几页"用
	var total int64
	db.Model(&Post{}).Count(&total)

	// 4. 分页查询（新的在前，按 id 倒序）：
	//    Offset((page-1)*size) = 跳过前面几页的帖子（第 2 页每页 20 条 → 跳过 20 条）
	//    Limit(size)           = 最多拿几条
	var posts []Post
	db.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&posts)

	// 5. 补上每条的作者、点赞数、评论数（列表里必须有这三个信息）
	fillAuthors(posts)
	fillCounts(posts)

	// 6. 成功信封：items 是本页帖子数组，meta 是分页信息
	ok(c, gin.H{
		"items": posts,
		"meta": gin.H{
			"page":      page,  // 当前第几页
			"page_size": size,  // 每页几条
			"total":     total, // 一共多少条
		},
	})
}

// ========== 看帖子详情：GET /api/v1/posts/:post_id ==========
// 详情 = 帖子本身 + 它名下的所有评论（按发表时间正序，从早到晚）
func getPost(c *gin.Context) {
	// 1. 解析路径参数：/api/v1/posts/5 时 post_id=5
	postID := parsePostID(c)

	// 2. 找到这条帖子
	var post Post
	if postID == 0 || db.First(&post, postID).Error != nil {
		fail(c, http.StatusNotFound, "帖子不存在") // 404 = 找不到
		return
	}

	// 3. 补上作者和两个计数
	db.First(&post.Author, post.UserID)
	fillCounts([]Post{post})

	// 4. 查它名下的评论（id 正序 = 发表时间从早到晚）
	var comments []Comment
	db.Where("post_id = ?", post.ID).Order("id ASC").Find(&comments)
	// 评论也要带上"谁评论的"——同样的套路：收集编号 → 一次查齐 → 填回去
	commentIDs := make([]uint, 0, len(comments))
	for _, cm := range comments {
		commentIDs = append(commentIDs, cm.UserID)
	}
	var users []User
	db.Where("id IN ?", commentIDs).Find(&users)
	userMap := make(map[uint]User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	for i := range comments {
		comments[i].Author = userMap[comments[i].UserID]
	}

	// 5. 组装详情响应：帖子四件套 + 评论数组
	ok(c, gin.H{
		"id":            post.ID,
		"content":       post.Content,
		"author":        post.Author,
		"like_count":    post.LikeCount,
		"comment_count": post.CommentCount,
		"created_at":    post.CreatedAt,
		"comments":      comments,
	})
}

// ========== 删除自己的帖子（要登录 + 只能删自己的）：DELETE /api/v1/posts/:post_id ==========
func deletePost(c *gin.Context) {
	// 1. 取路径参数和当前登录用户
	postID := parsePostID(c)
	uid := c.GetUint("uid")

	// 2. 找到这条帖子
	var post Post
	if postID == 0 || db.First(&post, postID).Error != nil {
		fail(c, http.StatusNotFound, "帖子不存在")
		return
	}

	// 3. 权限检查：这条帖子是我的吗？
	if post.UserID != uid {
		// 403 = 你登录了，但没权限动它
		fail(c, http.StatusForbidden, "无权删除他人的帖子")
		return
	}

	// 4. 删除（连同它的评论和点赞一起清掉）
	if err := cascadeDeletePost(post.ID); err != nil {
		fail(c, http.StatusInternalServerError, "删除失败")
		return
	}

	// 5. 删除成功：data 为 null（没东西可返回）
	ok(c, nil)
}

// ========== 发评论（要登录）：POST /api/v1/posts/:post_id/comment ==========
func createComment(c *gin.Context) {
	// 1. 帖子存在吗？先找到才能评论
	postID := parsePostID(c)
	var post Post
	if postID == 0 || db.First(&post, postID).Error != nil {
		fail(c, http.StatusNotFound, "帖子不存在")
		return
	}

	// 2. 解析 + 校验评论内容（1 ~ 1000 字）
	var input struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if len(input.Content) < 1 || len(input.Content) > 1000 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 3. 组装评论：评论者从 token 取（和发帖同一个套路）
	comment := Comment{
		PostID:    postID,
		UserID:    c.GetUint("uid"),
		Content:   input.Content,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := db.Omit("Author").Create(&comment).Error; err != nil {
		fail(c, http.StatusInternalServerError, "评论失败")
		return
	}

	// 4. 填上评论者信息再回复 201
	db.First(&comment.Author, comment.UserID)
	created(c, comment)
}

// ========== 点赞 / 取消点赞：POST /api/v1/posts/:post_id/like ==========
// 开关式设计：点一下点赞，再点一下取消（再次请求 = 取消）
func toggleLike(c *gin.Context) {
	// 1. 帖子存在吗？
	postID := parsePostID(c)
	var post Post
	if postID == 0 || db.First(&post, postID).Error != nil {
		fail(c, http.StatusNotFound, "帖子不存在")
		return
	}

	uid := c.GetUint("uid")

	// 2. 我点过赞吗？查点赞表
	var like Like
	err := db.Where("user_id = ? AND post_id = ?", uid, postID).First(&like).Error

	isLiked := true
	if err == nil {
		// 已点过 → 删掉这条记录 = 取消点赞
		db.Delete(&like)
		isLiked = false
	} else {
		// 没点过 → 新建记录 = 点赞
		// （数据库有联合唯一索引兜底，就算重复请求也不会插出两条）
		db.Create(&Like{UserID: uid, PostID: postID})
	}

	// 3. 回复当前状态：还赞着吗？
	ok(c, gin.H{
		"post_id":  postID,
		"is_liked": isLiked,
	})
}

// ========== 批量查点赞状态：POST /api/v1/posts/likes ==========
// 一次问多个帖子："我分别赞过它们吗？"（列表页渲染"已赞"小红心用）
func batchLikeStatus(c *gin.Context) {
	// 1. 接收帖子编号数组
	var input struct {
		PostIDs []int `json:"post_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 2. 校验：不能为空、最多 100 个（防恶意一次问几万个）、编号必须是正整数
	if len(input.PostIDs) == 0 || len(input.PostIDs) > 100 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}
	for _, id := range input.PostIDs {
		if id < 1 {
			fail(c, http.StatusBadRequest, "参数校验失败")
			return
		}
	}

	// 3. 一条 SQL 查出"我赞过其中哪些帖子"
	uid := c.GetUint("uid")
	var likes []Like
	db.Where("user_id = ? AND post_id IN ?", uid, input.PostIDs).Find(&likes)
	likedSet := make(map[uint]bool, len(likes)) // 集合：我赞过的帖子编号
	for _, l := range likes {
		likedSet[l.PostID] = true
	}

	// 4. 按提问顺序逐个回答
	status := make([]gin.H, 0, len(input.PostIDs))
	for _, id := range input.PostIDs {
		status = append(status, gin.H{
			"post_id": id,
			"liked":   likedSet[uint(id)],
		})
	}
	ok(c, gin.H{"status": status})
}

// ========== 管理员删除任意帖子：DELETE /api/v1/admin/posts/:post_id ==========
// 和普通删除的区别：不管帖子是谁发的，管理员都能删
func adminDeletePost(c *gin.Context) {
	// 1. 先验身份：token 里的角色必须是 admin
	//    （authMiddleware 已经把 role 存进上下文了，这里取出来核对）
	if c.GetString("role") != RoleAdmin {
		fail(c, http.StatusForbidden, "仅管理员可删除任意帖子")
		return
	}

	// 2. 帖子存在吗？
	postID := parsePostID(c)
	var post Post
	if postID == 0 || db.First(&post, postID).Error != nil {
		fail(c, http.StatusNotFound, "帖子不存在")
		return
	}

	// 3. 删除（连同评论和点赞），data 为 null
	if err := cascadeDeletePost(post.ID); err != nil {
		fail(c, http.StatusInternalServerError, "删除失败")
		return
	}
	ok(c, nil)
}
