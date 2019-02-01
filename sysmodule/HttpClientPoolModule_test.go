package sysmodule_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/duanhf2012/origin/sysmodule"
	_ "github.com/go-sql-driver/mysql"
)

func TestHttpClientPoolModule(t *testing.T) {
	c := sysmodule.HttpClientPoolModule{}
	c.Init(10)

	rsp := c.Request(http.MethodGet, "https://www.baidu.com/", nil)
	fmt.Println(rsp.Err)
	fmt.Println(rsp.Header)
	fmt.Println(rsp.StatusCode)
	fmt.Println(rsp.Status)
	fmt.Println(string(rsp.Body))

	srsp := c.SyncRequest(http.MethodGet, "https://www.baidu.com/", nil)
	rsp1 := srsp.Get(1)
	fmt.Println(rsp1.Err)
	fmt.Println(rsp1.Header)
	fmt.Println(rsp1.StatusCode)
	fmt.Println(rsp1.Status)
	fmt.Println(string(rsp1.Body))
}

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
		AAAA    string `json:"-"`
	}{}
	err := sysmodule.NewSQLDecoder(res).SetSpecificTag("json").SetStrictMode(true).UnMarshal(&out)
	if err != nil {
		t.Error(err)
	}

	sres := db.SyncQuery("select * from tbl_fun_heelthrow where id >= 1")
	res = sres.Get(1000)
	if res.Err != nil {
		t.Error(res.Err)
	}
}
