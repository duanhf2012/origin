package sysmodule_test

import (
	"sync"
	"testing"

	"github.com/duanhf2012/origin/sysmodule"
	_ "github.com/go-sql-driver/mysql"
)

func TestDBModule(t *testing.T) {
	db := sysmodule.DBModule{}
	db.ExitChan = make(chan bool)
	db.WaitGroup = new(sync.WaitGroup)

	db.Init(100, "192.168.0.5:3306", "root", "Root!!2018", "QuantFundsDB")
	db.OnInit()
	res := db.Query("select * from tbl_fun_heelthrow where id >= 1")
	if res.Err != nil {
		t.Error(res.Err)
	}
	out := []struct {
		Addtime int64  `json:"addtime"`
		Tname   string `json:"tname"`
		Uuid    string `json:"uuid,omitempty"`
		AAAA    string `json:"xxx"`
	}{}
	err := res.UnMarshal(&out)
	if err != nil {
		t.Error(err)
	}

	sres := db.SyncQuery("select * from tbl_fun_heelthrow where id >= 1")
	res = sres.Get(2000)
	if res.Err != nil {
		t.Error(res.Err)
	}

	out2 := []struct {
		Addtime int64  `json:"addtime"`
		Tname   string `json:"tname"`
		Uuid    string `json:"uuid,omitempty"`
		AAAA    string `json:"xxx"`
	}{}

	err = res.UnMarshal(&out2)
	if err != nil {
		t.Error(err)
	}
}
