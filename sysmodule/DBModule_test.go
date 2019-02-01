package sysmodule_test

import (
	"testing"

	"github.com/duanhf2012/origin/sysmodule"
	_ "github.com/go-sql-driver/mysql"
)

func TestDBModule(t *testing.T) {
	db := sysmodule.DBModule{
		URL:      "192.168.0.5:3306",
		UserName: "root",
		Password: "Root!!2018",
		DBName:   "QuantFundsDB",
	}
	db.Connect()

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
	err := res.SetSpecificTag("json").SetBlurMode(false).UnMarshal(&out)
	if err != nil {
		t.Error(err)
	}

	sres := db.SyncQuery("select * from tbl_fun_heelthrow where id >= 1")
	res = sres.Get(1000)
	if res.Err != nil {
		t.Error(res.Err)
	}
}
