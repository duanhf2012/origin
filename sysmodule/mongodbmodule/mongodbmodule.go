package mongodbmodule

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
	"time"
)

type MongoModule struct {
	client             *mongo.Client
	maxOperatorTimeOut time.Duration
}

type Session struct {
	*mongo.Client
	maxOperatorTimeOut time.Duration
}

func (mm *MongoModule) Init(uri string, maxOperatorTimeOut time.Duration) error {
	var err error
	mm.client, err = mongo.NewClient(options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}

	mm.maxOperatorTimeOut = maxOperatorTimeOut
	return nil
}

func (mm *MongoModule) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mm.client.Connect(ctx); err != nil {
		return err
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mm.client.Ping(ctxTimeout, nil); err != nil {
		return err
	}

	return nil
}

func (mm *MongoModule) TakeSession() Session {
	return Session{Client: mm.client, maxOperatorTimeOut: mm.maxOperatorTimeOut}
}

func (s *Session) CountDocument(db string, collection string) (int64, error) {
	ctxTimeout, cancel := s.GetDefaultContext()
	defer cancel()
	return s.Database(db).Collection(collection).CountDocuments(ctxTimeout, bson.D{})
}

func (s *Session) NextSeq(db string, collection string, id interface{}) (int, error) {
	var res struct {
		Seq int
	}

	ctxTimeout, cancel := s.GetDefaultContext()
	defer cancel()
	err := s.Client.Database(db).Collection(collection).FindOneAndUpdate(ctxTimeout, bson.M{"_id": id}, bson.M{"$inc": bson.M{"Seq": 1}}).Decode(&res)
	return res.Seq, err
}

//indexKeys[索引][每个索引key字段]
func (s *Session) EnsureIndex(db string, collection string, indexKeys [][]string, bBackground bool) error {
	return s.ensureIndex(db, collection, indexKeys, bBackground, false)
}

//indexKeys[索引][每个索引key字段]
func (s *Session) EnsureUniqueIndex(db string, collection string, indexKeys [][]string, bBackground bool) error {
	return s.ensureIndex(db, collection, indexKeys, bBackground, true)
}

//keys[索引][每个索引key字段]
func (s *Session) ensureIndex(db string, collection string, indexKeys [][]string, bBackground bool, unique bool) error {
	keysDoc := bsonx.Doc{}

	var indexes []mongo.IndexModel
	for _, keys := range indexKeys {
		for _, key := range keys {
			keysDoc = keysDoc.Append(key, bsonx.Int32(1))
		}

		indexes = append(indexes, mongo.IndexModel{
			Keys:    keysDoc,
			Options: options.Index().SetUnique(unique).SetBackground(bBackground),
		})
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), s.maxOperatorTimeOut)
	defer cancel()
	_, err := s.Database(db).Collection(collection).Indexes().CreateMany(ctxTimeout, indexes)
	return err
}

func (s *Session) GetDefaultContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.maxOperatorTimeOut)
}
