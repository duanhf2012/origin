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
)

// DBModule ...
type DBModule struct {
	db       *sql.DB
	URL      string
	UserName string
	Password string
	DBName   string
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

// Next ...
func (slf *DBResult) Next() bool {
	if slf.Err != nil {
		return false
	}
	return slf.res.Next()
}

// Scan ...
func (slf *DBResult) Scan(arg ...interface{}) error {
	if slf.Err != nil {
		return slf.Err
	}
	return slf.res.Scan(arg...)
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
	fmt.Println(tbm)
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
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				integer64, err := strconv.ParseInt(meta, 10, 64)
				if err != nil {
					return err
				}
				value.SetInt(integer64)
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
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
func (slf *DBModule) Connect() error {
	cmd := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8&parseTime=true&loc=%s",
		slf.UserName,
		slf.Password,
		slf.URL,
		slf.DBName,
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
	return nil
}

// SyncDBResult ...
type SyncDBResult struct {
	sres chan DBResult
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

// Query ...
func (slf *DBModule) Query(query string, args ...interface{}) DBResult {
	rows, err := slf.db.Query(query, args...)
	return DBResult{
		Err: err,
		res: rows,
	}
}

// SyncQuery ...
func (slf *DBModule) SyncQuery(query string, args ...interface{}) SyncDBResult {
	ret := SyncDBResult{
		sres: make(chan DBResult, 1),
	}
	go func() {
		rsp := slf.Query(query, args...)
		ret.sres <- rsp
	}()
	return ret
}

// Exec ...
func (slf *DBModule) Exec(query string, args ...interface{}) DBResult {
	ret := DBResult{}
	res, err := slf.db.Exec(query, args...)
	ret.Err = err
	ret.LastInsertID, _ = res.LastInsertId()
	ret.RowsAffected, _ = res.RowsAffected()
	return ret
}

// SyncExec ...
func (slf *DBModule) SyncExec(query string, args ...interface{}) SyncDBResult {
	ret := SyncDBResult{
		sres: make(chan DBResult, 1),
	}
	go func() {
		rsp := slf.Exec(query, args...)
		ret.sres <- rsp
	}()
	return ret
}
