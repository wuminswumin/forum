// 学生论坛版：注册、登录
// 核心知识：密码哈希（bcrypt）+ 登录凭证（JWT）+ 角色枚举

package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5" // JWT：登录后发的"盖章通行证"
	"golang.org/x/crypto/bcrypt"   // bcrypt：密码哈希算法（单向搅碎机）
)

// ========== 角色枚举：Go 的正确写法 ==========
// Go 没有 enum 关键字，惯例是用"常量组"定义枚举——好处：全代码统一用常量名，不会拼错字符串
const (
	RoleStudent = "student" // 学生
	RoleAdmin   = "admin"   // 管理员
)

// jwtSecret 是签名密钥：服务器用它给通行证盖章，别人伪造不了
// 真实项目里这串字符必须随机、保密、不能写死在代码里（我们教学先这样）
var jwtSecret = []byte("wumin-forum-secret-2026")

// tokenTTL 登录凭证有效期（秒）：2 小时，和验收标准示例里的 expires_in: 7200 对齐
const tokenTTL = 7200

// adminAllowlist 管理员白名单：只有这些预留学号能注册成 admin
// 相当于学校提前预留的"管理员账号"，以后要加管理员就往这里添学号。
// 这也是验收标准的要求："生产环境应限制管理员账户创建来源"
// ⚠️ 注意：验收方测试管理员功能时，要用白名单里的学号注册（如 20240001）
var adminAllowlist = map[string]bool{
	"20240001": true,
	"20240002": true,
	"20240003": true,
}

// isAllDigits 检查字符串是否全由数字组成（学号校验用）
func isAllDigits(s string) bool {
	for _, ch := range s { // range 会把字符串拆成一个一个字符（Go 里叫 rune）
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// ========== 注册：POST /api/v1/auth/register ==========
func register(c *gin.Context) {
	// 1. 接收数据：学号 + 姓名 + 密码 + 角色
	var input struct {
		Username string `json:"username"` // 学号（账号）
		Name     string `json:"name"`     // 姓名
		Password string `json:"password"` // 密码
		// 枚举约束用 binding 标签声明：
		//   oneof=student admin  → 只允许这两个值，其它值自动 400
		//   omitempty            → 允许不传（空值跳过校验，后面代码里补默认 student）
		Role string `json:"role" binding:"omitempty,oneof=student admin"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 2. 基本检查（按验收标准：学号纯数字 1~32 位，姓名 1~32 位，密码 8~16 位）
	input.Username = strings.TrimSpace(input.Username)
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Username) < 1 || len(input.Username) > 32 || !isAllDigits(input.Username) {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}
	if len(input.Name) < 1 || len(input.Name) > 32 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}
	if len(input.Password) < 8 || len(input.Password) > 16 {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 3. 角色处理：没传 → 默认学生；要当管理员 → 检查白名单
	//    管理员账号只能由预留学号注册（见 adminAllowlist），防止任何人自封管理员
	role := input.Role
	if role == "" {
		role = RoleStudent // 用常量，不用手写 "student"（这就是常量组的好处）
	}
	if role == RoleAdmin && !adminAllowlist[input.Username] {
		// 403 = 登录了但没权限（这里指该学号不在管理员预留名单里）
		fail(c, http.StatusForbidden, "该学号没有管理员注册权限")
		return
	}

	// 4. 查重：学号已存在？标准要求这种情况返回 409（冲突）
	var count int64
	db.Model(&User{}).Where("username = ?", input.Username).Count(&count)
	if count > 0 {
		fail(c, http.StatusConflict, "用户名已存在")
		return
	}

	// 5. 密码哈希：把密码"搅碎"再存！
	//    明文密码绝不进数据库——万一数据库泄露，黑客拿到的是乱码，
	//    无法反推出你的真实密码（哈希是单向的，搅进去就搅不回来）
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, "注册失败")
		return
	}

	// 6. 创建用户
	user := User{
		Username:     input.Username, // 学号
		Name:         input.Name,     // 姓名
		PasswordHash: string(hash),   // 存的是"搅碎后的"，不是明文
		Role:         role,           // 校验过的角色
		CreatedAt:    time.Now().Format(time.RFC3339),
	}
	if err := db.Create(&user).Error; err != nil {
		fail(c, http.StatusInternalServerError, "注册失败")
		return
	}

	// 7. 回复 201 + 成功信封。
	//    注意：PasswordHash 和 CreatedAt 带 json:"-" 标签，转 JSON 时自动隐藏，
	//    外面只能看到验收标准要求的 id/username/name/role 四件套
	created(c, user)
}

// ========== 登录：POST /api/v1/auth/login ==========
func login(c *gin.Context) {
	// 1. 接收数据
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "参数校验失败")
		return
	}

	// 2. 按学号找用户
	var user User
	result := db.Where("username = ?", input.Username).First(&user)
	if result.Error != nil {
		// 找不到人。故意用含糊说法，不告诉别人"是学号错还是密码错"（防撞库）
		fail(c, http.StatusUnauthorized, "账号或密码错误")
		return
	}

	// 3. 校验密码：把输入的密码搅碎，和库存的搅碎结果比对
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password))
	if err != nil {
		fail(c, http.StatusUnauthorized, "账号或密码错误")
		return
	}

	// 4. 密码对了！发"盖章通行证"（JWT）
	//    claims 是通行证上写的内容：你是谁 + 什么角色 + 有效期（2 小时后作废）
	claims := jwt.MapClaims{
		"uid":      user.ID,                          // 你的编号
		"username": user.Username,                    // 你的学号
		"role":     user.Role,                        // 你的角色
		"exp":      time.Now().Add(tokenTTL * time.Second).Unix(), // 过期时间
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(jwtSecret) // 用密钥盖章签名
	if err != nil {
		fail(c, http.StatusInternalServerError, "登录失败")
		return
	}

	// 5. 回复凭证 + 身份信息，套验收标准的数据结构：
	//    access_token = 通行证本身；token_type 固定 "Bearer"；expires_in = 有效期秒数
	ok(c, gin.H{
		"access_token": signed,
		"token_type":   "Bearer",
		"expires_in":   tokenTTL,
		"user": gin.H{ // 前端需要知道角色来决定显示什么界面
			"id":       user.ID,
			"username": user.Username,
			"name":     user.Name,
			"role":     user.Role,
		},
	})
}

// ========== 门卫（中间件）：夹在"路由"和"处理函数"之间的检查关卡 ==========
//   请求 → 【门卫检查】→ 通过才放行 → 处理函数干活
// 注册方式：r.POST("/api/v1/posts", authMiddleware, createPost)
// 请求先过 authMiddleware 这一关，通过了才轮到 createPost
func authMiddleware(c *gin.Context) {
	// 1. 从请求头拿 Authorization 字段（客户端带通行证的标准位置）
	//    约定格式："Bearer <token>"，Bearer 意为"持证人"
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		// 门卫拒绝后要"拦下"（Abort），处理函数根本不会执行
		fail(c, http.StatusUnauthorized, "未登录或令牌无效")
		c.Abort()
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	// 2. 验证通行证：章是真的吗？过期了吗？
	//    ParseWithClaims = "拆开通行证，按我们的密钥验章"
	claims := jwt.MapClaims{} // 空地图：等会把通行证上的信息装进来
	token, err := jwt.ParseWithClaims(tokenStr, claims,
		func(t *jwt.Token) (interface{}, error) {
			// 这个回调函数告诉解析器："用这把密钥验章"
			// 伪造的 token 没有我们的章，这里就会验不过
			return jwtSecret, nil
		})
	if err != nil || !token.Valid {
		// err != nil：签名不对/格式坏；token.Valid == false：过期了
		fail(c, http.StatusUnauthorized, "未登录或令牌无效")
		c.Abort()
		return
	}

	// 3. 验证通过！把通行证上的用户信息存进"上下文"（c）
	//    后面的处理函数就能取出来用
	c.Set("uid", uint(claims["uid"].(float64))) // 注意：JWT 里的数字解析出来是 float64，要转成 uint
	c.Set("username", claims["username"].(string))
	// role 用"带 ok 的断言"：老 token 里没有 role 字段时不会崩，而是拿到空字符串
	role, _ := claims["role"].(string)
	c.Set("role", role)

	// 4. 放行，让请求继续去处理函数
	c.Next()
}
