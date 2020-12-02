package mongomodule

import (
	"fmt"
	_ "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"testing"
	"time"
)



type Student struct {
	ID    bson.ObjectId `bson:"_id"`
	Name   string  `bson: "name"`
	Age    int     `bson: "age"`
	Sid    string  `bson: "sid"`
	Status int     `bson: "status"`
}

func Test_Example(t *testing.T) {
	module:=MongoModule{}
	module.Init("mongodb://admin:123456@127.0.0.1:27017",100, 5*time.Second,5*time.Second)

	// take session
	s := module.Take()
	c := s.DB("test2").C("t_student")

	//2.定义对象
	insertData := Student{
		ID:bson.NewObjectId(),
		Name:   "seeta11",
		Age:    35, //*^_^*
		Sid:    "s20180907",
		Status: 1,
	}

	updateData := Student{
		Name:   "seeta11",
		Age:    18,
		Sid:    "s20180907",
		Status: 1,
	}

	//3.插入数据
	err := c.Insert(&insertData)

	//4.查找数据
	selector := bson.M{"_id":bson.ObjectIdHex("5f25303e999c622d361989b0")}
	m:=Student{}
	err = c.Find(selector).One(&m)

	//5.更新数据
	//selector2 := bson.M{"_id":bson.ObjectIdHex("5f25303e999c622d361989b0")}
	updateData.ID = bson.ObjectIdHex("5f25303e999c622d361989b0")
	err = c.UpdateId(bson.ObjectIdHex("5f25303e999c622d361989b0"),&updateData)
	if err != nil {
		fmt.Print(err)
	}

	//6.删除数据
	err = c.RemoveId(bson.ObjectIdHex("5f252f09999c622d36198951"))
	if err != nil {
		fmt.Print(err)
	}

	//7.序号自增
	s.EnsureCounter("test2","t_student","5f252f09999c622d36198951")
	for i := 0; i < 3; i++ {
		id, err := s.NextSeq("test2", "t_student", "5f252f09999c622d36198951")
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(id)
	}

	//8.setoninsert使用
	info,uErr := c.Upsert(bson.M{"_id":bson.ObjectIdHex("5f252f09999c622d36198951")},bson.M{
		"$setOnInsert":bson.M{"Name":"setoninsert","Age":55}})
	fmt.Println(info,uErr)
}
