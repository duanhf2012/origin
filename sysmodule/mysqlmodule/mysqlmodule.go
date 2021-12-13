package mysqlmondule

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duanhf2012/origin/service"
	_ "github.com/go-sql-driver/mysql"
)

type SyncFun func()

type DBExecute struct {
	syncExecuteFun   chan SyncFun
	syncExecuteExit  chan bool
}

type PingExecute struct {
	tickerPing *time.Ticker
	pintExit   chan bool
}

// DBModule ...
type MySQLModule struct {
	service.Module
	db               *sql.DB
	url              string
	username         string
	password         string
	dbname           string
	slowDuration        time.Duration
	pingCoroutine 	 PingExecute
	waitGroup    	 sync.WaitGroup
}

// Tx ...
type Tx struct {
	tx        *sql.Tx
	slowDuration time.Duration
}

// DBResult ...
type DBResult struct {
	LastInsertID int64
	RowsAffected int64

	rowNum  int
	RowInfo map[string][]interface{} //map[fieldname][row]sql.NullString
}

type DataSetList struct {
	dataSetList       []DBResult
	currentDataSetIdx int32
	tag               string
	blur              bool
}


type dbControl interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
}


func (m *MySQLModule) Init( url string, userName string, password string, dbname string,maxConn int) error {
	m.url = url
	m.username = userName
	m.password = password
	m.dbname = dbname
	m.pingCoroutine = PingExecute{tickerPing : time.NewTicker(5*time.Second), pintExit : make(chan bool, 1)}

	return m.connect(maxConn)
}

func (m *MySQLModule) SetQuerySlowTime(slowDuration time.Duration) {
	m.slowDuration = slowDuration
}

func (m *MySQLModule) Query(strQuery string, args ...interface{}) (*DataSetList, error) {
	return query(m.slowDuration, m.db,strQuery,args...)
}

// Exec ...
func (m *MySQLModule) Exec(strSql string, args ...interface{}) (*DBResult, error) {
	return exec(m.slowDuration, m.db,strSql,args...)
}

// Begin starts a transaction.
func (m *MySQLModule) Begin() (*Tx, error) {
	var txDBModule Tx
	txDb, err := m.db.Begin()
	if err != nil {
		log.Error("Begin error:%s", err.Error())
		return &txDBModule, err
	}
	txDBModule.slowDuration = m.slowDuration
	txDBModule.tx = txDb
	return &txDBModule, nil
}

// Rollback aborts the transaction.
func (slf *Tx) Rollback() error {
	return slf.tx.Rollback()
}

// Commit commits the transaction.
func (slf *Tx) Commit() error {
	return slf.tx.Commit()
}

// QueryEx executes a query that return rows.
func (slf *Tx) Query(strQuery string, args ...interface{}) (*DataSetList, error) {
	return query(slf.slowDuration,slf.tx,strQuery,args...)
}

// Exec executes a query that doesn't return rows.
func (slf *Tx) Exec(strSql string, args ...interface{}) (*DBResult, error) {
	return exec(slf.slowDuration,slf.tx,strSql,args...)
}

// Connect ...
func (m *MySQLModule) connect(maxConn int) error {
	cmd := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8&parseTime=true&loc=%s&readTimeout=30s&timeout=15s&writeTimeout=30s",
		m.username,
		m.password,
		m.url,
		m.dbname,
		url.QueryEscape(time.Local.String()))

	db, err := sql.Open("mysql", cmd)
	if err != nil {
		return err
	}
	err = db.Ping()
	if err != nil {
		db.Close()
		return err
	}
	m.db = db
	db.SetMaxOpenConns(maxConn)
	db.SetMaxIdleConns(maxConn)
	db.SetConnMaxLifetime(time.Second * 90)

	go m.runPing()

	return nil
}

func (m *MySQLModule) runPing() {
	for {
		select {
		case <-m.pingCoroutine.pintExit:
			log.Error("RunPing stopping %s...", fmt.Sprintf("%T", m))
			return
		case <-m.pingCoroutine.tickerPing.C:
			if m.db != nil {
				m.db.Ping()
			}
		}
	}
}

func  checkArgs(args ...interface{}) error {
	for _, val := range args {
		if reflect.TypeOf(val).Kind() == reflect.String {
			retVal := val.(string)
			if strings.Contains(retVal, "-") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(retVal, "#") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(retVal, "&") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(retVal, "=") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(retVal, "%") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(retVal, "'") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(strings.ToLower(retVal), "delete ") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(strings.ToLower(retVal), "truncate ") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(strings.ToLower(retVal), " or ") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(strings.ToLower(retVal), "from ") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
			if strings.Contains(strings.ToLower(retVal), "set ") == true {
				return fmt.Errorf("CheckArgs is error arg is %+v", retVal)
			}
		}
	}

	return nil
}

func checkSlow(slowDuration time.Duration,Time time.Duration) bool {
	if slowDuration != 0 && Time >=slowDuration {
		return true
	}
	return false
}

func query(slowDuration time.Duration,db dbControl,strQuery string, args ...interface{}) (*DataSetList, error) {
	datasetList := DataSetList{}
	datasetList.tag = "json"
	datasetList.blur = true

	if checkArgs(args) != nil {
		log.Error("CheckArgs is error :%s", strQuery)
		return &datasetList, fmt.Errorf("CheckArgs is error!")
	}

	if db == nil {
		log.Error("cannot connect database:%s", strQuery)
		return &datasetList, fmt.Errorf("cannot connect database!")
	}

	TimeFuncStart := time.Now()
	rows, err := db.Query(strQuery, args...)
	timeFuncPass := time.Since(TimeFuncStart)

	if checkSlow(slowDuration,timeFuncPass) {
		log.Error("DBModule QueryEx Time %s , Query :%s , args :%+v", timeFuncPass, strQuery, args)
	}
	if err != nil {
		log.Error("Query:%s(%v)", strQuery, err)
		if rows != nil {
			rows.Close()
		}
		return &datasetList, err
	}
	defer rows.Close()

	for {
		dbResult := DBResult{}
		//取出当前结果集所有行
		for rows.Next() {
			if dbResult.RowInfo == nil {
				dbResult.RowInfo = make(map[string][]interface{})
			}
			//RowInfo map[string][][]sql.NullString //map[fieldname][row][column]sql.NullString
			colField, err := rows.Columns()
			if err != nil {
				return &datasetList, err
			}
			count := len(colField)
			valuePtrs := make([]interface{}, count)
			for i := 0; i < count; i++ {
				valuePtrs[i] = &sql.NullString{}
			}
			rows.Scan(valuePtrs...)

			for idx, fieldname := range colField {
				fieldRowData := dbResult.RowInfo[strings.ToLower(fieldname)]
				fieldRowData = append(fieldRowData, valuePtrs[idx])
				dbResult.RowInfo[strings.ToLower(fieldname)] = fieldRowData
			}
			dbResult.rowNum += 1
		}

		datasetList.dataSetList = append(datasetList.dataSetList, dbResult)
		//取下一个结果集
		hasRet := rows.NextResultSet()

		if hasRet == false {
			if rows.Err() != nil {
				log.Error( "Query:%s(%+v)", strQuery, rows)
			}
			break
		}
	}

	return &datasetList, nil
}

func exec(slowDuration time.Duration,db dbControl,strSql string, args ...interface{}) (*DBResult, error) {
	ret := &DBResult{}
	if db == nil {
		log.Error("cannot connect database:%s", strSql)
		return ret, fmt.Errorf("cannot connect database!")
	}

	if checkArgs(args) != nil {
		log.Error("CheckArgs is error :%s", strSql)
		return ret, fmt.Errorf("CheckArgs is error!")
	}

	TimeFuncStart := time.Now()
	res, err := db.Exec(strSql, args...)
	timeFuncPass := time.Since(TimeFuncStart)
	if checkSlow(slowDuration,timeFuncPass) {
		log.Error("DBModule QueryEx Time %s , Query :%s , args :%+v", timeFuncPass, strSql, args)
	}
	if err != nil {
		log.Error("Exec:%s(%v)", strSql, err)
		return nil, err
	}

	ret.LastInsertID, _ = res.LastInsertId()
	ret.RowsAffected, _ = res.RowsAffected()

	return ret, nil
}

func (ds *DataSetList) UnMarshal(args ...interface{}) error {
	if len(ds.dataSetList) != len(args) {
		return errors.New(fmt.Sprintf("Data set len(%d,%d) is not equal to args!", len(ds.dataSetList), len(args)))
	}

	for _, out := range args {
		v := reflect.ValueOf(out)
		if v.Kind() != reflect.Ptr {
			return errors.New("interface must be a pointer")
		}

		if v.Kind() != reflect.Ptr {
			return errors.New("interface must be a pointer")
		}

		if v.Elem().Kind() == reflect.Struct {
			err := ds.rowData2interface(0, ds.dataSetList[ds.currentDataSetIdx].RowInfo, v)
			if err != nil {
				return err
			}
		}
		if v.Elem().Kind() == reflect.Slice {
			err := ds.slice2interface(out)
			if err != nil {
				return err
			}
		}

		ds.currentDataSetIdx = ds.currentDataSetIdx + 1
	}

	return nil
}

func (ds *DataSetList) slice2interface(in interface{}) error {
	length := ds.dataSetList[ds.currentDataSetIdx].rowNum
	if length == 0 {
		return nil
	}

	v := reflect.ValueOf(in).Elem()
	newv := reflect.MakeSlice(v.Type(), 0, length)
	v.Set(newv)
	v.SetLen(length)

	for i := 0; i < length; i++ {
		idxv := v.Index(i)
		if idxv.Kind() == reflect.Ptr {
			newObj := reflect.New(idxv.Type().Elem())
			v.Index(i).Set(newObj)
			idxv = newObj
		} else {
			idxv = idxv.Addr()
		}

		err := ds.rowData2interface(i, ds.dataSetList[ds.currentDataSetIdx].RowInfo, idxv)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ds *DataSetList) rowData2interface(rowIdx int, m map[string][]interface{}, v reflect.Value) error {
	t := v.Type()
	val := v.Elem()
	typ := t.Elem()

	if !val.IsValid() {
		return errors.New("Incorrect data type!")
	}

	for i := 0; i < val.NumField(); i++ {
		value := val.Field(i)
		kind := value.Kind()
		tag := typ.Field(i).Tag.Get(ds.tag)
		if tag == "" {
			tag = typ.Field(i).Name
		}

		if tag != "" && tag != "-" {
			vtag := strings.ToLower(tag)
			columnData, ok := m[vtag]
			if ok == false {
				if !ds.blur {
					return fmt.Errorf("Cannot find filed name %s!", vtag)
				}
				continue
			}
			if len(columnData) <= rowIdx {
				return fmt.Errorf("datasource column is error %s", tag)
			}
			meta := columnData[rowIdx].(*sql.NullString)
			if !ok {
				if !ds.blur {
					return fmt.Errorf("No corresponding field was found in the result set %s!", tag)
				}
				continue
			}
			if !value.CanSet() {
				return errors.New("Struct fields do not have read or write permissions!")
			}

			if meta.Valid == false {
				continue
			}

			if len(meta.String) == 0 {
				continue
			}

			switch kind {
			case reflect.String:
				value.SetString(meta.String)
			case reflect.Float32, reflect.Float64:
				f, err := strconv.ParseFloat(meta.String, 64)
				if err != nil {
					return err
				}
				value.SetFloat(f)
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
				integer64, err := strconv.ParseInt(meta.String, 10, 64)
				if err != nil {
					return err
				}
				value.SetInt(integer64)
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
				integer64, err := strconv.ParseUint(meta.String, 10, 64)
				if err != nil {
					return err
				}
				value.SetUint(integer64)
			case reflect.Bool:
				b, err := strconv.ParseBool(meta.String)
				if err != nil {
					return err
				}
				value.SetBool(b)
			default:
				return errors.New("The database map has unrecognized data types!")
			}
		}
	}
	return nil
}
