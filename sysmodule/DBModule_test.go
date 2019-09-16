package sysmodule_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/duanhf2012/origin/sysmodule"
	_ "github.com/go-sql-driver/mysql"
)

func TestDBModule(t *testing.T) {
	db := sysmodule.DBModule{}
	db.ExitChan = make(chan bool)
	db.WaitGroup = new(sync.WaitGroup)

	// db.Init(100, "192.168.0.5:3306", "root", "Root!!2018", "QuantFundsDB")
	db.Init(100, "127.0.0.1:3306", "root", "zgh50221", "rebort_message")
	db.OnInit()
	tx, err := db.GetTx()
	if err != nil {
		fmt.Println("err 1", err)
		return
	}
	res, err := tx.QueryEx("select id as Id, info_type as InfoType, info_type_Name as InfoTypeName from tbl_info_type where id >= 1")
	if err != nil {
		fmt.Println("err 2", err)
		tx.Rollback()
		return
	}
	out := []struct {
		Id           int64
		InfoType     string
		InfoTypeName string
	}{}
	err = res.UnMarshal(&out)
	if err != nil {
		fmt.Println("err 3", err)
		tx.Rollback()
		return
	}
	fmt.Println(out)
	_, err = tx.Exec("insert into tbl_info_type(info_type, info_type_name) VALUES (?, ?)", "4", "weibo")
	if err != nil {
		fmt.Println("err 4", err)
		tx.Rollback()
		return
	}
	_, err = tx.Exec("update tbl_info_type set info_types = ? Where id = ?", "5", 0)
	if err != nil {
		fmt.Println("err 4", err)
		tx.Rollback()
		return
	}

	tx.Commit()
	// res, err := db.QueryEx("select * from tbl_fun_heelthrow where id >= 1")
	// if err != nil {
	// 	t.Error(err)
	// }
	// out := []struct {
	// 	Addtime int64  `json:"addtime"`
	// 	Tname   string `json:"tname"`
	// 	Uuid    string `json:"uuid,omitempty"`
	// 	AAAA    string `json:"xxx"`
	// }{}
	// err = res.UnMarshal(&out)
	// if err != nil {
	// 	t.Error(err)
	// }

	// sres := db.SyncQuery("select * from tbl_fun_heelthrow where id >= 1")
	// res, err = sres.Get(2000)
	// if err != nil {
	// 	t.Error(err)
	// }

	// out2 := []struct {
	// 	Addtime int64  `json:"addtime"`
	// 	Tname   string `json:"tname"`
	// 	Uuid    string `json:"uuid,omitempty"`
	// 	AAAA    string `json:"xxx"`
	// }{}

	// err = res.UnMarshal(&out2)
	// if err != nil {
	// 	t.Error(err)
	// }
}
