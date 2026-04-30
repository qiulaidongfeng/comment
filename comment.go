package comment

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-viper/encoding/ini"
	"github.com/libtnb/sqlite"
	"github.com/mileusna/useragent"
	"github.com/spf13/viper"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type status int8

const (
	// 默认显示评论内容
	Active status = iota
	// 隐藏评论内容
	Block
)

// 一条评论内容
type Entry struct {
	Content string
	Time    time.Time
	Ip      string
	// ReplyID 可以为空，不为空表示回复之前某条评论
	ID, ReplyID, BlogID string
	Status              status
	UserAgent           string
	// 故意在每一条评论包含作者信息，便与区分不同设备发出的评论
	// 且当作者修改信息时，历史评论的信息依然能够保持不变
	// 同时保证只使用一条SQL就能获取一篇blog的所有评论
	Author AuthorInfo `gorm:"embedded"`
}

type AuthorInfo struct {
	Name      string
	Email     string
	Device_ID string `gorm:"column:Device_ID"`
}

var v = newv()

var host string = v.GetString("comment.host")
var allow_origin string = v.GetString("comment.allowOrigin")

func newv() *viper.Viper {
	codecRegistry := viper.NewCodecRegistry()
	codecRegistry.RegisterCodec("ini", ini.Codec{})
	v := viper.NewWithOptions(viper.WithCodecRegistry(codecRegistry))
	v.SetConfigFile("config.ini")
	v.AutomaticEnv()
	v.WatchConfig()
	err := v.ReadInConfig()
	if err != nil {
		panic(err)
	}
	return v
}

func Handle(s *gin.Engine) {
	s.Use(func(ctx *gin.Context) {
		ua := ctx.GetHeader("User-Agent")
		u := useragent.Parse(ua)
		if u.Name == "" || u.OS == "" {
			ctx.String(http.StatusForbidden, "User-Agent验证未通过")
			ctx.Abort()
			return
		}
		//TODO:进行更多验证
		if (ctx.Request.Host != host && ctx.Request.Host != "") ||
			(!strings.Contains(ctx.Request.Header.Get("Accept"), "text/html") && !strings.Contains(ctx.Request.Header.Get("Accept"), "*/*")) ||
			!strings.Contains(ctx.Request.Header.Get("Accept-Encoding"), "gzip") ||
			ctx.Request.Header.Get("Accept-Language") == "" ||
			ctx.Request.Header.Get("Sec-Fetch-Site") == "" {
			ctx.String(http.StatusForbidden, "浏览器完整性验证未通过")
			ctx.Abort()
			return
		}
	})
	s.Use(func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		if strings.Contains(allow_origin, origin) {
			ctx.Header("Access-Control-Allow-Origin", origin)
		}
		ctx.Header("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		ctx.Header("Access-Control-Allow-Headers", "Content-Type,bid")
		ctx.Header("Access-Control-Max-Age", "86400")
		if ctx.Request.Method == "OPTIONS" {
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}
	})
	s.POST("/send_comment", func(ctx *gin.Context) {
		if !limit(ctx) {
			return
		}
		send(ctx)
	})
	s.GET("/comment", func(ctx *gin.Context) {
		list(ctx)
	})
}

func send(ctx *gin.Context) {
	comment := ctx.PostForm("comment")
	if comment == "" {
		ctx.String(http.StatusBadRequest, "评论内容不能为空")
		return
	}
	name := ctx.PostForm("name")
	if name == "" {
		ctx.String(http.StatusBadRequest, "您的昵称不能为空")
		return
	}
	email := ctx.PostForm("email")
	if email == "" {
		ctx.String(http.StatusBadRequest, "您的邮箱不能为空")
		return
	}
	if !strings.Contains(email, ".") || !strings.Contains(email, "@") {
		ctx.String(http.StatusBadRequest, "您的邮箱格式不正确")
		return
	}
	Device_ID := ctx.PostForm("device_id")
	if Device_ID == "" {
		ctx.String(http.StatusBadRequest, "您的设备id不能为空")
		return
	}
	bid := ctx.PostForm("bid")
	if bid == "" {
		ctx.String(http.StatusBadRequest, "发送给的blog不能为空")
		return
	}
	rid := ctx.PostForm("rid")
	remoteip := ctx.Request.Context().Value("remote_trust_ip").(string)
	authorInfo := AuthorInfo{
		Name:      name,
		Email:     email,
		Device_ID: Device_ID,
	}

	// 每个邮箱和昵称必须唯一绑定
	// 可以通过不同的设备使用
	// 同一设备不能使用多个邮箱或昵称

	//TODO：和实际写入在同一事务
	var author AuthorInfo
	err := db.Where("email = ?", email).Take(&author).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		panic(err)
	}
	if err != gorm.ErrRecordNotFound && !equalAuthor(author, authorInfo) {
		ctx.String(http.StatusConflict, "此邮箱已经有其他昵称")
		return
	}

	var author2 AuthorInfo
	err = db.Where("name = ?", name).Take(&author2).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		panic(err)
	}
	if err != gorm.ErrRecordNotFound && !equalAuthor(author2, authorInfo) {
		ctx.String(http.StatusConflict, "此昵称已经有其他邮箱使用")
		return
	}

	var author3 AuthorInfo
	err = db.Where("Device_ID = ?", Device_ID).Take(&author3).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		panic(err)
	}
	if err != gorm.ErrRecordNotFound && !equalAuthor(author3, authorInfo) {
		ctx.String(http.StatusConflict, "此设备已经使用过其他邮箱和昵称")
		return
	}

	c := &Entry{
		Content:   comment,
		Ip:        remoteip,
		ID:        Genid(),
		ReplyID:   rid,
		BlogID:    bid,
		Author:    authorInfo,
		UserAgent: ctx.Request.UserAgent(),
		Time:      time.Now(),
	}
	mu.Lock()
	defer mu.Unlock()
	err = db.Transaction(func(tx *gorm.DB) error {
		err = tx.Create(c).Error
		if err != nil {
			panic(err)
		}
		err = tx.FirstOrCreate(&authorInfo).Error
		if err != nil {
			panic(err)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	ctx.String(200, "发送评论成功")
}

var ipCount = make(map[string]int)

// 限制每5分钟，只能发送5条评论
func limit(ctx *gin.Context) bool {
	mu.Lock()
	defer mu.Unlock()
	rip := ctx.Request.Context().Value("remote_trust_ip").(string)
	if c := ipCount[rip]; c == 5 {
		ctx.String(http.StatusForbidden, "发送评论太频繁，此ip已被封禁5分钟")
		ctx.Abort()
		return false
	}
	ipCount[rip] = ipCount[rip] + 1
	if ipCount[rip] == 5 {
		time.AfterFunc(5*time.Minute, func() {
			mu.Lock()
			defer mu.Unlock()
			delete(ipCount, rip)
		})
	}
	return true
}

// 返回特定博客的所有未隐藏评论
func list(ctx *gin.Context) {
	var comment []Entry
	bid := ctx.Request.Header.Get("bid")
	if bid == "" {
		ctx.String(http.StatusBadRequest, "需要bid")
		return
	}
	err := db.Where("blog_id = ?", bid).Where("status = ?", Active).Select("name", "id", "content", "reply_id", "time").Find(&comment).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		panic(err)
	}
	if err == gorm.ErrRecordNotFound {
		ctx.Status(404)
		return
	}
	ctx.JSON(200, comment)
}

var mu sync.Mutex

var db = func() *gorm.DB {
	db, err := gorm.Open(sqlite.Open("comment.db?_pragma=journal_mode(WAL)"), &gorm.Config{SkipDefaultTransaction: true})
	err = db.AutoMigrate(&id{})
	if err != nil {
		panic(err)
	}
	err = db.AutoMigrate(&Entry{})
	if err != nil {
		panic(err)
	}
	err = db.AutoMigrate(&AuthorInfo{})
	if err != nil {
		panic(err)
	}
	return db
}()

type id struct {
	ID int64 `gorm:"default:0;primaryKey;autoIncrement:false"`
}

// 生成全局递增的id
func Genid() string {
	mu.Lock()
	defer mu.Unlock()
	// Note:故意使用事务，便于未来可能迁移到其他数据库
	i := &id{} // 加锁查询
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Take(i).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Create(i).Error
			}
			return err
		}
		return tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Model(i).Where(i, "ID").Update("id", i.ID+1).Error
	})
	if err != nil {
		panic(err)
	}
	return strconv.Itoa(int(i.ID))
}

func equalAuthor(a, b AuthorInfo) bool {
	return a.Email == b.Email && a.Name == b.Name
}
