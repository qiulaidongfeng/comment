package comment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
)

var s *gin.Engine

func TestMain(M *testing.M) {
	s = gin.Default()
	Handle(s)
	M.Run()
	s, err := db.DB()
	if err != nil {
		panic(err)
	}
	err = s.Close()
	if err != nil {
		panic(err)
	}
	err = os.Remove("comment.db")
	if err != nil {
		panic(err)
	}
}

func TestGenid(t *testing.T) {
	testGenid(t, "0")
	testGenid(t, "1")
	testGenid(t, "2")
	var mu sync.Mutex
	m := make(map[string]struct{})
	var wg sync.WaitGroup
	for range 10000 {
		wg.Go(func() {
			got := Genid()
			v, err := strconv.Atoi(got)
			if err != nil {
				panic(err)
			}
			if v < 3 || v > 10003 {
				t.Fatal(v)
			}
			mu.Lock()
			defer mu.Unlock()
			if _, ok := m[got]; ok {
				t.Fatalf("dup")
			}
			m[got] = struct{}{}
		})
	}
	wg.Wait()
}

func testGenid(t *testing.T, want string) {
	got := Genid()
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestSendCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		send_req(t, "", "", "", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "评论内容不能为空" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "", "", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "您的昵称不能为空" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "name", "", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "您的邮箱不能为空" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "name", "d", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "您的邮箱格式不正确" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "name", "a@a.com", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "您的设备id不能为空" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "name", "a@a.com", "", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "发送评论太频繁，此ip已被封禁5分钟" {
				return errors.New(string(s))
			}
			return nil
		})
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		send_req(t, "c", "name", "a@a.com", "1", "", "", "", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "发送给的blog不能为空" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "c", "1", "a@a.com", "1", "1", "", "127.0.0.1", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "发送评论成功" {
				return errors.New(string(s))
			}
			return nil
		})
		send_req(t, "2", "1", "a@a.com", "1", "1", "", "127.0.0.1", func(r *httptest.ResponseRecorder) error {
			s, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if string(s) != "发送评论成功" {
				return errors.New(string(s))
			}
			return nil
		})
		err := db.Model(&Entry{}).Where("content = ?", "2").Update("status", Block).Error
		if err != nil {
			panic(err)
		}
		// 此2个test case与上面的有顺序依赖
		send_req(t, "1", "name", "a@a.com", "1", "1", "", "127.0.0.1", func(r *httptest.ResponseRecorder) error {
			if r.Code != 409 || r.Body.String() != "此邮箱已经有其他昵称" {
				return fmt.Errorf("%d %s", r.Code, r.Body.String())
			}
			return nil
		})
		send_req(t, "1", "1", "b@a.com", "1", "1", "", "127.0.0.1", func(r *httptest.ResponseRecorder) error {
			if r.Code != 409 || r.Body.String() != "此昵称已经有其他邮箱使用" {
				return fmt.Errorf("%d %s", r.Code, r.Body.String())
			}
			return nil
		})
		send_req(t, "1", "2", "b@a.com", "1", "1", "", "127.0.0.1", func(r *httptest.ResponseRecorder) error {
			if r.Code != 409 || r.Body.String() != "此设备已经使用过其他邮箱和昵称" {
				return fmt.Errorf("%d %s", r.Code, r.Body.String())
			}
			return nil
		})
		send_req_list(t, "1", func(r *httptest.ResponseRecorder) error {
			s := r.Body.Bytes()
			var a []Entry
			err := json.Unmarshal(s, &a)
			if err != nil {
				return err
			}
			if len(a) != 1 {
				return fmt.Errorf("%v", a)
			}
			m := a[0]
			if m.Content != "c" || time.Since(m.Time) > time.Second || m.UserAgent != Ua || m.ID != "10003" || m.BlogID != "1" ||
				m.Status != Active || m.Author.Name != "1" ||
				m.Author.Email != "a@a.com" || m.Author.Device_ID != "1" {
				return errors.New(r.Body.String())
			}
			return nil
		})
		send_req_list(t, "3", func(r *httptest.ResponseRecorder) error {
			s := r.Body.Bytes()
			var a []Entry
			err := json.Unmarshal(s, &a)
			if err != nil {
				return err
			}
			if len(a) != 0 {
				return errors.New(r.Body.String())
			}
			return nil
		})
	})
}

func send_req(t *testing.T, comment, name, email, device_id, bid, rid, rip string, check func(r *httptest.ResponseRecorder) error) {
	req, err := http.NewRequest("POST", "/send_comment", nil)
	if err != nil {
		panic(err)
	}
	req.PostForm = make(url.Values)
	req.PostForm.Add("comment", comment)
	req.PostForm.Add("name", name)
	req.PostForm.Add("email", email)
	req.PostForm.Add("device_id", device_id)
	req.PostForm.Add("bid", bid)
	req.PostForm.Add("rid", rid)
	addInfo(req)
	req = req.WithContext(context.WithValue(req.Context(), "remote_trust_ip", rip))
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, req)
	err = check(r)
	if err != nil {
		t.Helper()
		t.Error(err)
	}
}

func send_req_list(t *testing.T, bid string, check func(r *httptest.ResponseRecorder) error) {
	req, err := http.NewRequest("GET", "/comment", nil)
	if err != nil {
		panic(err)
	}
	req.Header.Add("bid", bid)
	addInfo(req)
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, req)
	err = check(r)
	if err != nil {
		t.Helper()
		t.Error(err)
	}
}

var Ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"

func addInfo(r *http.Request) {
	r.Header.Add("User-Agent", Ua)
	r.Header.Add("Accept", "text/html")
	r.Header.Add("Accept-Encoding", "gzip")
	r.Header.Add("Accept-Language", "zh-CN,zh;q=0.9")
	r.Header.Add("Sec-Fetch-Site", "same-origin")
	r.Host = "127.0.0.1"
	r.AddCookie(&http.Cookie{Name: "history"})
}
