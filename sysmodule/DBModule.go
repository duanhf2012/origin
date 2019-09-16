package sysmodule

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/service"
	_ "github.com/go-sql-driver/mysql"
)

type SyncFun func()

const (
	MAX_EXECUTE_FUN = 10000
)

// DBModule ...
type DBModule struct {
	service.BaseModule
	db               *sql.DB
	url              string
	username         string
	password         string
	dbname           string
	maxconn          int
	PrintTime        time.Duration
	syncExecuteFun   chan SyncFun
	syncCoroutineNum int
}

// Tx ...
type Tx struct {
	tx        *sql.Tx
	PrintTime time.Duration
}

// DBResult ...
type DBResult struct {
	Err          error
	LastInsertID int64
	RowsAffected int64
	res          *sql.Rows
	// 解码数据相关设置
	tag  string
	blur bool
}

// DBResult ...
type DBResultEx struct {
	LastInsertID int64
	RowsAffected int64

	rowNum  int
	RowInfo map[string][]interface{} //map[fieldname][row]sql.NullString
}

type DataSetList struct {
	dataSetList       []DBResultEx
	currentDataSetIdx int32
	tag               string
	blur              bool
}

// SyncDBResult ...
type SyncDBResult struct {
	sres chan DBResult
}

type SyncQueryDBResultEx struct {
	sres chan *DataSetList
	err  chan error
}

type SyncExecuteDBResult struct {
	sres chan *DBResultEx
	err  chan error
}

func (slf *DBModule) OnRun() bool {
	if slf.db != nil {
		slf.db.Ping()

	}
	time.Sleep(time.Second * 5)
	return true
}
func (slf *DBModule) Init(maxConn int, url string, userName string, password string, dbname string) error {
	slf.url = url
	slf.maxconn = maxConn
	slf.username = userName
	slf.password = password
	slf.dbname = dbname
	slf.syncExecuteFun = make(chan SyncFun, MAX_EXECUTE_FUN)

	return slf.Connect(slf.maxconn)
}

func (slf *DBModule) OnInit() error {
	for i := 0; i < slf.syncCoroutineNum; i++ {
		go slf.RunExecuteDBCoroutine()
	}

	return nil
}

//Close ...
func (slf *DBResult) Close() {
	if slf.res != nil {
		slf.res.Close()
	}
}

//NextResult ...
func (slf *DBResult) NextResult() bool {
	if slf.Err != nil || slf.res == nil {
		return false
	}
	return slf.res.NextResultSet()
}

// SetSpecificTag ...
func (slf *DBResult) SetSpecificTag(tag string) *DBResult {
	slf.tag = tag
	return slf
}

// SetBlurMode ...
func (slf *DBResult) SetBlurMode(blur bool) *DBResult {
	slf.blur = blur
	return slf
}

// UnMarshal ...
func (slf *DBResult) UnMarshal(out interface{}) error {
	if slf.Err != nil {
		return slf.Err
	}
	tbm, err := dbResult2Map(slf.res)
	if err != nil {
		return err
	}
	//fmt.Println(tbm)
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Ptr {
		return errors.New("interface must be a pointer")
	}
	if v.Elem().Kind() == reflect.Struct {
		if len(tbm) != 1 {
			return fmt.Errorf("数据结果集的长度不匹配 len=%d", len(tbm))
		}
		return slf.mapSingle2interface(tbm[0], v)
	}
	if v.Elem().Kind() == reflect.Slice {
		return slf.mapSlice2interface(tbm, out)
	}
	return fmt.Errorf("错误的数据类型 %v", v.Elem().Kind())
}

func dbResult2Map(rows *sql.Rows) ([]map[string]string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	count := len(columns)
	tableData := make([]map[string]string, 0)
	values := make([]string, count)
	valuePtrs := make([]interface{}, count)
	for rows.Next() {
		for i := 0; i < count; i++ {
			valuePtrs[i] = &values[i]
		}
		err := rows.Scan(valuePtrs...)
		if err != nil {
			fmt.Println(err)
		}
		entry := make(map[string]string)
		for i, col := range columns {
			entry[strings.ToLower(col)] = values[i]
		}
		tableData = append(tableData, entry)
	}
	return tableData, nil
}

func (slf *DBResult) mapSingle2interface(m map[string]string, v reflect.Value) error {
	t := v.Type()
	val := v.Elem()
	typ := t.Elem()

	if !val.IsValid() {
		return errors.New("数据类型不正确")
	}

	for i := 0; i < val.NumField(); i++ {
		value := val.Field(i)
		kind := value.Kind()
		tag := typ.Field(i).Tag.Get(slf.tag)
		if tag == "" {
			tag = typ.Field(i).Name
		}

		if tag != "" && tag != "-" {
			vtag := strings.Split(strings.ToLower(tag), ",")
			meta, ok := m[vtag[0]]
			if !ok {
				if !slf.blur {
					return fmt.Errorf("没有在结果集中找到对应的字段 %s", tag)
				}
				continue
			}
			if !value.CanSet() {
				return errors.New("结构体字段没有读写权限")
			}
			if len(meta) == 0 {
				continue
			}
			switch kind {
			case reflect.String:
				value.SetString(meta)
			case reflect.Float32, reflect.Float64:
				f, err := strconv.ParseFloat(meta, 64)
				if err != nil {
					return err
				}
				value.SetFloat(f)
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
				integer64, err := strconv.ParseInt(meta, 10, 64)
				if err != nil {
					return err
				}
				value.SetInt(integer64)
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
				integer64, err := strconv.ParseUint(meta, 10, 64)
				if err != nil {
					return err
				}
				value.SetUint(integer64)
			case reflect.Bool:
				b, err := strconv.ParseBool(meta)
				if err != nil {
					return err
				}
				value.SetBool(b)
			default:
				return errors.New("数据库映射存在不识别的数据类型")
			}
		}
	}
	return nil
}

func (slf *DBModule) SetQuerySlowTime(Time time.Duration) {
	slf.PrintTime = Time
}

func (slf *DBModule) IsPrintTimeLog(Time time.Duration) bool {
	if slf.PrintTime != 0 && Time >= slf.PrintTime {
		return true
	}
	return false
}

func (slf *DBResult) mapSlice2interface(data []map[string]string, in interface{}) error {
	length := len(data)

	if length > 0 {
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
			err := slf.mapSingle2interface(data[i], idxv)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Connect ...
func (slf *DBModule) Connect(maxConn int) error {
	cmd := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8&parseTime=true&loc=%s&readTimeout=30s&timeout=15s&writeTimeout=30s",
		slf.username,
		slf.password,
		slf.url,
		slf.dbname,
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
	slf.db = db
	db.SetMaxOpenConns(maxConn)
	db.SetMaxIdleConns(maxConn)
	db.SetConnMaxLifetime(time.Second * 90)

	slf.syncCoroutineNum = maxConn

	return nil
}

// Get ...
func (slf *SyncDBResult) Get(timeoutMs int) DBResult {
	timerC := time.NewTicker(time.Millisecond * time.Duration(timeoutMs)).C
	select {
	case <-timerC:
		break
	case rsp := <-slf.sres:
		return rsp
	}
	return DBResult{
		Err: fmt.Errorf("Getting the return result timeout [%d]ms", timeoutMs),
	}
}

func (slf *SyncQueryDBResultEx) Get(timeoutMs int) (*DataSetList, error) {
	timerC := time.NewTicker(time.Millisecond * time.Duration(timeoutMs)).C
	select {
	case <-timerC:
		break
	case err := <-slf.err:
		dataset := <-slf.sres
		return dataset, err
	}

	return nil, fmt.Errorf("Getting the return result timeout [%d]ms", timeoutMs)
}

func (slf *DBModule) CheckArgs(args ...interface{}) error {
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

// Query ...
func (slf *DBModule) Query(query string, args ...interface{}) DBResult {
	if slf.CheckArgs(args) != nil {
		ret := DBResult{}
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		ret.Err = fmt.Errorf("CheckArgs is error!")
		return ret
	}

	if slf.db == nil {
		ret := DBResult{}
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		ret.Err = fmt.Errorf("cannot connect database!")
		return ret
	}
	rows, err := slf.db.Query(query, args...)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Query:%s(%v)", query, err)
	}

	return DBResult{
		Err:  err,
		res:  rows,
		tag:  "json",
		blur: true,
	}
}

func (slf *DBModule) QueryEx(query string, args ...interface{}) (*DataSetList, error) {
	datasetList := DataSetList{}
	datasetList.tag = "json"
	datasetList.blur = true

	if slf.CheckArgs(args) != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		return &datasetList, fmt.Errorf("CheckArgs is error!")
	}

	if slf.db == nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		return &datasetList, fmt.Errorf("cannot connect database!")
	}

	TimeFuncStart := time.Now()
	rows, err := slf.db.Query(query, args...)
	TimeFuncPass := time.Since(TimeFuncStart)

	if slf.IsPrintTimeLog(TimeFuncPass) {
		service.GetLogger().Printf(service.LEVER_INFO, "DBModule QueryEx Time %s , Query :%s , args :%+v", TimeFuncPass, query, args)
	}
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Query:%s(%v)", query, err)
		if rows != nil {
			rows.Close()
		}
		return &datasetList, err
	}
	defer rows.Close()

	for {
		dbResult := DBResultEx{}
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
				service.GetLogger().Printf(service.LEVER_ERROR, "Query:%s(%+v)", query, rows)
			}
			break
		}
	}

	return &datasetList, nil
}

// SyncQuery ...
func (slf *DBModule) SyncQuery(query string, args ...interface{}) SyncQueryDBResultEx {
	ret := SyncQueryDBResultEx{
		sres: make(chan *DataSetList, 1),
		err:  make(chan error, 1),
	}

	if len(slf.syncExecuteFun) >= MAX_EXECUTE_FUN {
		dbret := DataSetList{}
		ret.err <- fmt.Errorf("chan is full,sql:%s", query)
		ret.sres <- &dbret

		return ret
	}

	slf.syncExecuteFun <- func() {
		rsp, err := slf.QueryEx(query, args...)
		ret.err <- err
		ret.sres <- rsp
	}

	return ret
}

// Exec ...
func (slf *DBModule) Exec(query string, args ...interface{}) (*DBResultEx, error) {
	ret := &DBResultEx{}
	if slf.db == nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		return ret, fmt.Errorf("cannot connect database!")
	}

	if slf.CheckArgs(args) != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		//return ret, fmt.Errorf("cannot connect database!")
		return ret, fmt.Errorf("CheckArgs is error!")
	}

	TimeFuncStart := time.Now()
	res, err := slf.db.Exec(query, args...)
	TimeFuncPass := time.Since(TimeFuncStart)
	if slf.IsPrintTimeLog(TimeFuncPass) {
		service.GetLogger().Printf(service.LEVER_INFO, "DBModule QueryEx Time %s , Query :%s , args :%+v", TimeFuncPass, query, args)
	}
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Exec:%s(%v)", query, err)
		return nil, err
	}

	ret.LastInsertID, _ = res.LastInsertId()
	ret.RowsAffected, _ = res.RowsAffected()

	return ret, nil
}

// SyncExec ...
func (slf *DBModule) SyncExec(query string, args ...interface{}) *SyncExecuteDBResult {
	ret := &SyncExecuteDBResult{
		sres: make(chan *DBResultEx, 1),
		err:  make(chan error, 1),
	}

	if len(slf.syncExecuteFun) >= MAX_EXECUTE_FUN {
		ret.err <- fmt.Errorf("chan is full,sql:%s", query)
		return ret
	}

	slf.syncExecuteFun <- func() {
		rsp, err := slf.Exec(query, args...)
		if err != nil {
			ret.err <- err
			return
		}

		ret.sres <- rsp
		return
	}

	return ret
}

func (slf *DBModule) RunExecuteDBCoroutine() {
	slf.WaitGroup.Add(1)
	defer slf.WaitGroup.Done()
	for {
		select {
		case <-slf.ExitChan:
			service.GetLogger().Printf(LEVER_WARN, "stopping module %s...", fmt.Sprintf("%T", slf))
			return
		case fun := <-slf.syncExecuteFun:
			fun()
		}
	}

}

func (slf *DataSetList) UnMarshal(args ...interface{}) error {
	if len(slf.dataSetList) != len(args) {
		return errors.New(fmt.Sprintf("Data set len(%d,%d) is not equal to args!", len(slf.dataSetList), len(args)))
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
			err := slf.rowData2interface(0, slf.dataSetList[slf.currentDataSetIdx].RowInfo, v)
			if err != nil {
				return err
			}
		}
		if v.Elem().Kind() == reflect.Slice {
			err := slf.slice2interface(out)
			if err != nil {
				return err
			}
		}

		slf.currentDataSetIdx = slf.currentDataSetIdx + 1
	}

	return nil
}

func (slf *DataSetList) slice2interface(in interface{}) error {
	length := slf.dataSetList[slf.currentDataSetIdx].rowNum
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

		err := slf.rowData2interface(i, slf.dataSetList[slf.currentDataSetIdx].RowInfo, idxv)
		if err != nil {
			return err
		}
	}

	return nil
}

func (slf *DataSetList) rowData2interface(rowIdx int, m map[string][]interface{}, v reflect.Value) error {
	t := v.Type()
	val := v.Elem()
	typ := t.Elem()

	if !val.IsValid() {
		return errors.New("数据类型不正确")
	}

	for i := 0; i < val.NumField(); i++ {
		value := val.Field(i)
		kind := value.Kind()
		tag := typ.Field(i).Tag.Get(slf.tag)
		if tag == "" {
			tag = typ.Field(i).Name
		}

		if tag != "" && tag != "-" {
			vtag := strings.ToLower(tag)
			columnData, ok := m[vtag]
			if ok == false {
				if !slf.blur {
					return fmt.Errorf("Cannot find filed name %s", vtag)
				}
				continue
			}
			if len(columnData) <= rowIdx {
				return fmt.Errorf("datasource column is error %s", tag)
			}
			meta := columnData[rowIdx].(*sql.NullString)
			if !ok {
				if !slf.blur {
					return fmt.Errorf("没有在结果集中找到对应的字段 %s", tag)
				}
				continue
			}
			if !value.CanSet() {
				return errors.New("结构体字段没有读写权限")
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
				return errors.New("数据库映射存在不识别的数据类型")
			}
		}
	}
	return nil
}

func (slf *DBModule) GetTx() (*Tx, error) {
	var txDBMoudule Tx
	txdb, err := slf.db.Begin()
	if err != nil {
		return &txDBMoudule, err
	}
	txDBMoudule.tx = txdb
	return &txDBMoudule, nil
}

func (slf *Tx) Rollback() error {
	return slf.tx.Rollback()
}

func (slf *Tx) Commit() error {
	return slf.tx.Commit()
}

func (slf *Tx) CheckArgs(args ...interface{}) error {
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

func (slf *Tx) Query(query string, args ...interface{}) DBResult {
	if slf.CheckArgs(args) != nil {
		ret := DBResult{}
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		ret.Err = fmt.Errorf("CheckArgs is error!")
		return ret
	}

	if slf.tx == nil {
		ret := DBResult{}
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		ret.Err = fmt.Errorf("cannot connect database!")
		return ret
	}

	rows, err := slf.tx.Query(query, args...)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Query:%s(%v)", query, err)
	}

	return DBResult{
		Err:  err,
		res:  rows,
		tag:  "json",
		blur: true,
	}
}

func (slf *Tx) IsPrintTimeLog(Time time.Duration) bool {
	if slf.PrintTime != 0 && Time >= slf.PrintTime {
		return true
	}
	return false
}

func (slf *Tx) QueryEx(query string, args ...interface{}) (*DataSetList, error) {
	datasetList := DataSetList{}
	datasetList.tag = "json"
	datasetList.blur = true

	if slf.CheckArgs(args) != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		return &datasetList, fmt.Errorf("CheckArgs is error!")
	}

	if slf.tx == nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		return &datasetList, fmt.Errorf("cannot connect database!")
	}

	TimeFuncStart := time.Now()
	rows, err := slf.tx.Query(query, args...)
	TimeFuncPass := time.Since(TimeFuncStart)

	if slf.IsPrintTimeLog(TimeFuncPass) {
		service.GetLogger().Printf(service.LEVER_INFO, "DBModule Tx QueryEx Time %s , Query :%s , args :%+v", TimeFuncPass, query, args)
	}
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Tx Query:%s(%v)", query, err)
		if rows != nil {
			rows.Close()
		}
		return &datasetList, err
	}
	defer rows.Close()

	for {
		dbResult := DBResultEx{}
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
				service.GetLogger().Printf(service.LEVER_ERROR, "Query:%s(%+v)", query, rows)
			}
			break
		}
	}

	return &datasetList, nil
}

// Exec ...
func (slf *Tx) Exec(query string, args ...interface{}) (*DBResultEx, error) {
	ret := &DBResultEx{}
	if slf.tx == nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "cannot connect database:%s", query)
		return ret, fmt.Errorf("cannot connect database!")
	}

	if slf.CheckArgs(args) != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "CheckArgs is error :%s", query)
		//return ret, fmt.Errorf("cannot connect database!")
		return ret, fmt.Errorf("CheckArgs is error!")
	}

	TimeFuncStart := time.Now()
	res, err := slf.tx.Exec(query, args...)
	TimeFuncPass := time.Since(TimeFuncStart)
	if slf.IsPrintTimeLog(TimeFuncPass) {
		service.GetLogger().Printf(service.LEVER_INFO, "DBModule QueryEx Time %s , Query :%s , args :%+v", TimeFuncPass, query, args)
	}
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Exec:%s(%v)", query, err)
		return nil, err
	}

	ret.LastInsertID, _ = res.LastInsertId()
	ret.RowsAffected, _ = res.RowsAffected()

	return ret, nil
}
