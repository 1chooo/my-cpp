package csp_test

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
)

/*
* Pub/Sub
https://blog.wu-boy.com/2022/04/simple-publish-subscribe-pattern-in-golang/
*/

/* 首先第一步需要建立一個 Hub 用來接受多個 Subscriber，而這個 Hub structure 結構如下 */
type hub struct {
	sync.Mutex
	subs map[*subscriber]struct{}
}

// 透過 newHub 用 map 方式初始化 subcribers，用 map 原因就是之後要實作 unsubscribe 會比較方便。
func newHub() *hub {
	return &hub{
		subs: map[*subscriber]struct{}{},
	}
}

// 接著建立 subscriber 結構
type message struct {
	data []byte
}

/*其中 name 代表此 subscriber 名稱，*/
type subscriber struct {
	sync.Mutex

	name    string
	handler chan *message
	quit    chan struct{}
}

/*接著新增 run 函式，當成功 subscribe 後可以接受訊息，透過 for 跟 select 來接受 channel 訊息。*/
func (s *subscriber) run(ctx context.Context) {
	for {
		select {
		case msg := <-s.handler:
			log.Println(s.name, string(msg.data))
		case <-s.quit:
			return
		case <-ctx.Done():
			return
		}
	}
}

// 初始化單一 Subscriber
func newSubscriber(name string) *subscriber {
	return &subscriber{
		name:    name,
		handler: make(chan *message, 100),
		quit:    make(chan struct{}),
	}
}

/*
這邊需要注意，每個 subscriber 透過 buffer channel 來接受 Hub 傳送過來的訊息。
請大家根據系統情境來決定是否調整 buffer 大小。
初始化完成 subscriber，就需要將其丟進 Hub 內進行 subscribe。
透過 map 將 subscriber 儲存起來，並透過 goroutine 方式將其丟到背景接受訊息。
*/
// 後面還會改這func，完整的放後面
/*func (h *hub) subscribe(ctx context.Context, s *subscriber) error {
	h.Lock()
	h.subs[s] = struct{}{}
	h.Unlock()

	go s.run(ctx)

	return nil
}
*/
/*
接下來透過 Publisher 將訊息收進來並丟到全部 Subscriber。
上一個步驟已經看到 subscriber 實現了 run 函式接受 publisher 訊息。
底下看看如何實現 publish 訊息機制
*/
func (h *hub) publish(ctx context.Context, msg *message) error {
	h.Lock()
	for s := range h.subs {
		s.publish(ctx, msg)
	}
	h.Unlock()

	return nil
}

/*
透過 for 迴圈將所有 subscriber 讀出來並街訊息傳入即可。
接著看一下如何實現 subscriber 的 publish 方法

這邊透過 select 方式來確保整個 main 不會被 block 住，
假設訊息處理過慢，又不透過 select + default，則系統會被 block 住，故這邊大家要多注意。
*/
func (s *subscriber) publish(ctx context.Context, msg *message) {
	select {
	case <-ctx.Done():
		return
	case s.handler <- msg:
	default:
	}
}

// 能訂閱就要能夠取消，也就是該如何從 map 正確移除 subscriber。在 hub 內實現 unsubscribe 功能
func (h *hub) unsubscribe(ctx context.Context, s *subscriber) error {
	h.Lock()
	delete(h.subs, s)
	h.Unlock()
	close(s.quit)
	return nil
}

/*
除了透過 unsubscribe 之外，大家可以看到我們也支援了 context 方式來取消訂閱，
故如果開發者執行 cancel()，理論上也要可以取消訂閱，這邊我們可以修改 subscribe 函式
*/
func (h *hub) subscribe(ctx context.Context, s *subscriber) error {
	h.Lock()
	h.subs[s] = struct{}{}
	h.Unlock()

	go func() {
		select {
		case <-s.quit:
		case <-ctx.Done():
			h.Lock()
			delete(h.subs, s)
			h.Unlock()
		}
	}()

	go s.run(ctx)

	return nil
}

/*
請大家注意其中的 go func() 監聽了 ctx.Done()，
在程式任何地方執行了 cancel() 就可以刪除 subscriber 了，
而在 subscriber 結構內有一個 quit 通道，用來讓手動 unsubscribe 後，可以關閉該 channel，
讓原本的 goroutine 可以正常結束，不會造成系統 goroutine 持續變高。
*/

func (h *hub) subscribers() int {
	h.Lock()
	c := len(h.subs)
	h.Unlock()
	return c
}

func TestPubSub(t *testing.T) {
	ctx := context.Background()
	h := newHub()
	sub01 := newSubscriber("sub01")
	sub02 := newSubscriber("sub02")
	sub03 := newSubscriber("sub03")

	h.subscribe(ctx, sub01)
	h.subscribe(ctx, sub02)
	h.subscribe(ctx, sub03)

	_ = h.publish(ctx, &message{data: []byte("test01")})
	_ = h.publish(ctx, &message{data: []byte("test02")})
	_ = h.publish(ctx, &message{data: []byte("test03")})
	time.Sleep(1 * time.Second)

	h.unsubscribe(ctx, sub03)
	_ = h.publish(ctx, &message{data: []byte("test04")})
	_ = h.publish(ctx, &message{data: []byte("test05")})

	time.Sleep(1 * time.Second)

	//關閉剩下的goruntine避免goleak檢測不通過
	h.unsubscribe(ctx, sub01)
	h.unsubscribe(ctx, sub02)
}

/*
驗證看看出來的訊息是不是有按照我們的模式跑出結果
另外為了驗證全部的 goroutine 都可以正常關閉，用 go.uber.org/goleak 來撰寫測試驗證。
*/
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestSubscriber(t *testing.T) {
	ctx := context.Background()
	h := newHub()
	sub01 := newSubscriber("sub01")
	sub02 := newSubscriber("sub02")
	sub03 := newSubscriber("sub03")

	h.subscribe(ctx, sub01)
	h.subscribe(ctx, sub02)
	h.subscribe(ctx, sub03)

	assert.Equal(t, 3, h.subscribers())

	h.unsubscribe(ctx, sub01)
	h.unsubscribe(ctx, sub02)
	h.unsubscribe(ctx, sub03)

	assert.Equal(t, 0, h.subscribers())
}

// 測試使用 context 來 cancel subscriber
func TestCancelSubscriber(t *testing.T) {
	ctx := context.Background()
	h := newHub()
	sub01 := newSubscriber("sub01")
	sub02 := newSubscriber("sub02")
	sub03 := newSubscriber("sub03")

	h.subscribe(ctx, sub01)
	h.subscribe(ctx, sub02)
	ctx03, cancel := context.WithCancel(ctx)
	h.subscribe(ctx03, sub03)

	assert.Equal(t, 3, h.subscribers())

	// cancel subscriber 03
	cancel()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, h.subscribers())

	h.unsubscribe(ctx, sub01)
	h.unsubscribe(ctx, sub02)

	assert.Equal(t, 0, h.subscribers())
}
