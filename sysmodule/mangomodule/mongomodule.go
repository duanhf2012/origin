package mangomodule

import (
	"github.com/duanhf2012/origin/log"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"sync"
	"time"

	_ "gopkg.in/mgo.v2"
)

// session
type Session struct {
	*mgo.Session
}

type DialContext struct {
	sync.Mutex
	sessions []*Session
	sessionNum uint32
	takeSessionIdx uint32
}

type MangoModule struct {
	dailContext *DialContext
}

func (slf *MangoModule) Init(url string,sessionNum uint32,dialTimeout time.Duration, timeout time.Duration) error {
	var err error
	slf.dailContext, err = dialWithTimeout(url, sessionNum, dialTimeout*time.Second, timeout*time.Minute)

	return err
}

func (slf *MangoModule) Take() *Session{
	return slf.dailContext.Take()
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
	s.SetSyncTimeout(timeout)
	s.SetSocketTimeout(timeout)

	c := new(DialContext)

	// sessions
	c.sessions = make([]*Session, sessionNum)
	c.sessions[0] = &Session{s}
	for i:=uint32(1) ;i< sessionNum;i++{
		c.sessions[i] = &Session{s.New()}
	}

	c.sessionNum = sessionNum

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

func (c *DialContext) Take()*Session{
	c.Lock()
	idx := c.takeSessionIdx %c.sessionNum
	c.takeSessionIdx++
	c.Unlock()

	return c.sessions[idx]
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
func (s *Session) EnsureIndex(db string, collection string, key []string) error {
	return s.DB(db).C(collection).EnsureIndex(mgo.Index{
		Key:    key,
		Unique: false,
		Sparse: true,
	})
}

// goroutine safe
func (s *Session) EnsureUniqueIndex(db string, collection string, key []string) error {
	return s.DB(db).C(collection).EnsureIndex(mgo.Index{
		Key:    key,
		Unique: true,
		Sparse: true,
	})
}
