package mysqlmondule

import (
	"testing"
	"fmt"
	"time"
)

func Test_Example(t *testing.T) {
	//初始化连接
	test := MySQLModule{}
	err := test.Init("127.0.0.1:3306","root","123456","dbname",10)
	if err !=nil {
		fmt.Print(err)
		return
	}

	//设置1秒慢查询监控
	test.SetQuerySlowTime(1*time.Second)

	//查询语句
	//存储过程可以call sp_querytest(?,?,?)方式
	result,err := test.Query("select Id,Name from UserInfo where SvrAreaId=?",18)
	if err!=nil {
		fmt.Print(err)
	}else{
		dbRet := []struct {
			Id   int    `json:"Id"`
			Name     string `json:"Name"`
		}{}

		//从结构集中返序列化数据到结构体切片中，UnMarshal可以支持多个结果集
		err = result.UnMarshal(&dbRet)
		if err !=nil {
			fmt.Print(err)
		}
	}

	//exec sql
	//存储过程可以call sp_updatetest(?,?,?)方式
	execResult,execErr := test.Exec("update UserInfo set Name=? where Id=?","nickname",1000)
	if execErr!=nil {
		fmt.Print(execErr)
	}else{
		fmt.Print(*execResult)
	}

}