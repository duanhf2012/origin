package mongodbmodule

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"log"
	"testing"
	"time"
)

type Student struct {
	ID      primitive.ObjectID `bson:"_id"`
	Name    string             `bson: "name"`
	Age     int                `bson: "age"`
	Sid     string             `bson: "sid"`
	Status  int                `bson: "status"`
	MapData map[int64]int64    `bson: "maptest"`
}

type StudentName struct {
	Name string `bson: "name"`
}

func Test_Example(t *testing.T) {
	//0.初始化模块
	var mongoModule MongoModule
	err := mongoModule.Init("mongodb://admin:123456@192.168.2.15:27017/?authSource=admin&maxPoolSize=100&maxConnecting=2&connectTimeoutMS=10000&socketTimeoutMS=5000", time.Second*10)
	if err != nil {
		t.Log(err)
		return
	}
	mongoModule.Start()

	//1.创建索引
	session := mongoModule.TakeSession()
	var IndexKeys [][]string
	//分别建立number,name组合索引
	var key1 []string
	key1 = append(key1, "number", "name")
	//keyId为索引
	var key2 []string
	key2 = append(key2, "KeyId")

	IndexKeys = append(IndexKeys, key1, key2)
	session.EnsureIndex("testdb", "test2", IndexKeys, true)

	//2.插入数据
	//插入单行
	var s Student
	s.ID = primitive.NewObjectID()
	s.Age = 35
	s.Name = "xxx"

	ctx, cancel := session.GetDefaultContext()
	ret, err := session.Collection("testdb", "test2").InsertOne(ctx, s)
	cancel()
	insertId := ret.InsertedID.(primitive.ObjectID)
	log.Println(insertId.Hex(), err)

	//插入多行
	var ss []interface{}
	ctx, cancel = session.GetDefaultContext()
	for i := 0; i < 3; i++ {
		var s Student
		s.ID = primitive.NewObjectID()
		s.Age = i
		s.Name = fmt.Sprintf("name_%d", i)
		ss = append(ss, s)
	}
	manyRet, err := session.Collection("testdb", "test2").InsertMany(ctx, ss)
	cancel()
	log.Println(manyRet, err)

	//3.更新数据
	//update
	var sUpdate Student
	sUpdate.ID, _ = primitive.ObjectIDFromHex("62429c3b32a269dcbe0cdc7b")
	sUpdate.Age = 35
	sUpdate.Name = "xxxx555555"

	//update := bson.M{"$set": bson.M{"age": 35}}
	update := bson.M{"$set": sUpdate}
	ctx, cancel = session.GetDefaultContext()
	objectId, _ := primitive.ObjectIDFromHex("62429c3b32a269dcbe0cdc7b")
	updateResult, err := session.Collection("testdb", "test2").UpdateOne(ctx, bson.M{"_id": objectId}, update)
	cancel()
	log.Println("collection.UpdateOne:", updateResult, err)

	//upset
	var s_upset Student
	s_upset.ID, _ = primitive.ObjectIDFromHex("62429c3b32a269dcbe0cdc7e")
	s_upset.Name = "皇商xx"
	s_upset.Age = 35099
	s_upset.Sid = "Sid22"
	s_upset.MapData = make(map[int64]int64)
	s_upset.MapData[3434] = 13424234
	s_upset.MapData[444] = 656565656
	update = bson.M{"$set": s_upset}
	updateOpts := options.Update().SetUpsert(true)
	ctx, cancel = session.GetDefaultContext()

	updateResult, err = session.Collection("testdb", "test2").UpdateOne(ctx, bson.M{"_id": s_upset.ID}, update, updateOpts)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("collection.UpdateOne:", updateResult)

	//4.删除
	ctx, cancel = session.GetDefaultContext()
	Id, _ := primitive.ObjectIDFromHex("62429aa71b6445d1f5bf9aee")
	deleteResult, err := session.Collection("testdb", "test2").DeleteOne(ctx, bson.M{"_id": Id})
	cancel()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("collection.DeleteOne:", deleteResult)

	//5.查询
	//查询单条
	ctx, cancel = session.GetDefaultContext()

	var sel_One Student
	Ids, _ := primitive.ObjectIDFromHex("62429b13bbff8acf147ef8d7")
	err = session.Collection("testdb", "test2").FindOne(context.Background(), bson.M{"_id": Ids}).Decode(&sel_One)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("collection.FindOne: ", sel_One)

	//查询多条1
	ctx, cancel = session.GetDefaultContext()
	cur, err := session.Collection("testdb", "test2").Find(ctx, bson.M{})
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	if err := cur.Err(); err != nil {
		log.Fatal(err)
	}

	var sSlice []Student
	ctx, cancel = session.GetDefaultContext()
	err = cur.All(ctx, &sSlice)
	if err != nil {
		log.Fatal(err)
	}
	cur.Close(ctx)
	cancel()
	log.Println("collection.Find curl.All: ", sSlice)
	for _, one := range sSlice {
		log.Println(one)
	}

	//查询多条2
	cur, err = session.Collection("testdb", "test2").Find(context.Background(), bson.M{})
	if err != nil {
		log.Fatal(err)
	}
	if err := cur.Err(); err != nil {
		log.Fatal(err)
	}

	for cur.Next(context.Background()) {
		var s Student
		if err = cur.Decode(&s); err != nil {
			log.Fatal(err)
		}
		log.Println("collection.Find cur.Next:", s)
	}

	cur.Close(context.Background())

	//模糊查询
	cur, err = session.Collection("testdb", "test2").Find(context.Background(), bson.M{"name": primitive.Regex{Pattern: "xxx"}})
	if err != nil {
		log.Fatal(err)
	}
	if err := cur.Err(); err != nil {
		log.Fatal(err)
	}

	var sSlices []Student
	err = cur.All(context.Background(), &sSlices)
	if err != nil {
		log.Fatal(err)
	}
	cur.Close(context.Background())

	//6.获取数据总行数
	count, err := session.Collection("testdb", "test2").CountDocuments(context.Background(), bson.D{})
	if err != nil {
		log.Fatal(count)
	}
	log.Println("collection.CountDocuments:", count)

	//7.自动序号
	Id, _ = primitive.ObjectIDFromHex("62429b13bbff8acf147ef8d7")
	for i := 0; i < 10; i++ {
		seq, _ := session.NextSeq("testdb", "test2", Id)
		log.Println(seq)
	}
}
