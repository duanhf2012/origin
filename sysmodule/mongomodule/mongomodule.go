package mongomodule

import (
	"github.com/duanhf2012/origin/log"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"sync"
	"time"
	"container/heap"
	_ "gopkg.in/mgo.v2"
)

// session
type Session struct {
	*mgo.Session
	ref   int
	index int
}

type SessionHeap []*Session

func (h SessionHeap) Len() int {
	return len(h)
}

func (h SessionHeap) Less(i, j int) bool {
	return h[i].ref < h[j].ref
}

func (h SessionHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *SessionHeap) Push(s interface{}) {
	s.(*Session).index = len(*h)
	*h = append(*h, s.(*Session))
}

func (h *SessionHeap) Pop() interface{} {
	l := len(*h)
	s := (*h)[l-1]
	s.index = -1
	*h = (*h)[:l-1]
	return s
}

type DialContext struct {
	sync.Mutex
	sessions SessionHeap
}

type MongoModule struct {
	dailContext *DialContext
}

func (slf *MongoModule) Init(url string,sessionNum uint32,dialTimeout time.Duration, timeout time.Duration) error {
	var err error
	slf.dailContext, err = dialWithTimeout(url, sessionNum, dialTimeout, timeout)

	return err
}

func (slf *MongoModule) Ref() *Session{
	return slf.dailContext.Ref()
}

func (slf *MongoModule) UnRef(s *Session) {
	slf.dailContext.UnRef(s)
}

// goroutine safe
func dialWithTimeout(url string, sessionNum uint32, dialTimeout time.Duration, timeout time.Duration) (*DialContext, error) {
	if sessionNum <= 0 {
		sessionNum = 100
		log.Release("invalid sessionNum, reset to %v", sessionNum)
	}

	s, err := mgo.DialWithTimeout(url, dialTimeout)
	if err != nil {
		return nil, err
	}

	s.SetMode(mgo.Strong,true)
	s.SetSyncTimeout(timeout)
	s.SetSocketTimeout(timeout)

	c := new(DialContext)

	// sessions
	c.sessions = make(SessionHeap, sessionNum)
	c.sessions[0] = &Session{s, 0, 0}
	for i := 1; i < int(sessionNum); i++ {
		c.sessions[i] = &Session{s.New(), 0, i}
	}
	heap.Init(&c.sessions)

	return c, nil
}

// goroutine safe
func (c *DialContext) Close() {
	c.Lock()
	for _, s := range c.sessions {
		s.Close()
	}
	c.Unlock()
}

// goroutine safe
func (c *DialContext) Ref() *Session {
	c.Lock()
	s := c.sessions[0]
	if s.ref == 0 {
		s.Refresh()
	}
	s.ref++
	heap.Fix(&c.sessions, 0)
	c.Unlock()

	return s
}

// goroutine safe
func (c *DialContext) UnRef(s *Session) {
	if s == nil {
		return
	}
	c.Lock()
	s.ref--
	heap.Fix(&c.sessions, s.index)
	c.Unlock()
}


// goroutine safe
func (s *Session) EnsureCounter(db string, collection string, id string) error {
	err := s.DB(db).C(collection).Insert(bson.M{
		"_id": id,
		"seq": 0,
	})
	if mgo.IsDup(err) {
		return nil
	} else {
		return err
	}
}

// goroutine safe
func (s *Session) NextSeq(db string, collection string, id string) (int, error) {
	var res struct {
		Seq int
	}
	_, err := s.DB(db).C(collection).FindId(id).Apply(mgo.Change{
		Update:    bson.M{"$inc": bson.M{"seq": 1}},
		ReturnNew: true,
	}, &res)

	return res.Seq, err
}

// goroutine safe
func (s *Session) EnsureIndex(db string, collection string, key []string, bBackground bool) error {
	return s.DB(db).C(collection).EnsureIndex(mgo.Index{
		Key:    key,
		Unique: false,
		Sparse: true,
		Background: bBackground,
	})
}

// goroutine safe
func (s *Session) EnsureUniqueIndex(db string, collection string, key []string, bBackground bool) error {
	return s.DB(db).C(collection).EnsureIndex(mgo.Index{
		Key:    key,
		Unique: true,
		Sparse: true,
		Background: bBackground,
	})
}
