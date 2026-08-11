package mongodbmodule

import (
	"context"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// EnsureIndex 在默认数据库的 collection 上创建一个普通索引并返回实际索引名。
//
// keys 必须使用非空 bson.D 保留复合索引字段顺序；options 按顺序应用，后者覆盖前者。
// MongoDB 对相同定义的 CreateIndexes 操作具有幂等语义，因此该方法可在启动阶段重复调用。
func (module *Module) EnsureIndex(
	ctx context.Context,
	collection string,
	keys bson.D,
	options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error) {
	return module.ensureIndex(ctx, collection, keys, options)
}

// EnsureUniqueIndex 创建唯一索引，并保证调用方不能通过 options 把 Unique 覆盖为 false。
func (module *Module) EnsureUniqueIndex(
	ctx context.Context,
	collection string,
	keys bson.D,
	options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error) {
	options = append(options, mongooptions.Index().SetUnique(true))
	return module.ensureIndex(ctx, collection, keys, options)
}

// EnsureTTLIndex 为单个时间字段创建 TTL 索引。
//
// expireAfter 必须是非负、整秒且不超过 int32 秒；0 表示文档在字段指定时刻到期。
// 最终 ExpireAfterSeconds 由本方法强制写入，调用方 options 不能覆盖它。
func (module *Module) EnsureTTLIndex(
	ctx context.Context,
	collection string,
	field string,
	expireAfter time.Duration,
	options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error) {
	field = strings.TrimSpace(field)
	if field == "" || expireAfter < 0 || expireAfter%time.Second != 0 {
		return "", errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule TTL 字段不能为空且有效期必须是非负整秒")
	}
	seconds := expireAfter / time.Second
	if seconds > math.MaxInt32 {
		return "", errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule TTL 有效期超过 int32 秒范围")
	}
	options = append(options, mongooptions.Index().SetExpireAfterSeconds(int32(seconds)))
	return module.ensureIndex(ctx, collection, bson.D{{Key: field, Value: 1}}, options)
}

// EnsureIndexes 按输入顺序逐个创建索引，并返回已经成功创建的索引名。
//
// 顺序 CreateOne 可兼容不完整支持 CreateMany 的 MongoDB 兼容服务。中途失败时 names 保留
// 已成功部分，便于定位；indexes 为空时在验证运行状态和参数后返回空切片。
func (module *Module) EnsureIndexes(
	ctx context.Context,
	collection string,
	indexes ...mongo.IndexModel,
) ([]string, error) {
	if ctx == nil || strings.TrimSpace(collection) == "" {
		return nil, errs.ErrInvalidArgument
	}
	runtime, err := module.requireRuntime()
	if err != nil {
		return nil, err
	}
	database := module.databaseName()
	names := make([]string, 0, len(indexes))
	for _, model := range indexes {
		if !validIndexKeys(model.Keys) {
			return names, errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule 索引 Keys 必须是非空 bson.D")
		}
		name, createErr := runtime.createIndex(ctx, database, collection, model)
		if createErr != nil {
			return names, createErr
		}
		names = append(names, name)
	}
	return names, nil
}

func (module *Module) ensureIndex(
	ctx context.Context,
	collection string,
	keys bson.D,
	options []mongooptions.Lister[mongooptions.IndexOptions],
) (string, error) {
	if ctx == nil || strings.TrimSpace(collection) == "" || !validIndexKeys(keys) {
		return "", errs.ErrInvalidArgument
	}
	if err := validateListers(options); err != nil {
		return "", err
	}
	runtime, err := module.requireRuntime()
	if err != nil {
		return "", err
	}

	builder := mongooptions.Index()
	for _, option := range options {
		builder.Opts = append(builder.Opts, option.List()...)
	}
	return runtime.createIndex(ctx, module.databaseName(), collection, mongo.IndexModel{
		Keys:    keys,
		Options: builder,
	})
}

func validIndexKeys(keys any) bool {
	document, ok := keys.(bson.D)
	if !ok || len(document) == 0 {
		return false
	}
	for _, element := range document {
		if strings.TrimSpace(element.Key) == "" {
			return false
		}
	}
	return true
}

func validateListers[T any](options []mongooptions.Lister[T]) error {
	for _, option := range options {
		if option == nil || isNilValue(option) {
			return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule Option 不能为空")
		}
		for _, setter := range option.List() {
			if setter == nil {
				return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule Option Setter 不能为空")
			}
		}
	}
	return nil
}

func isNilValue(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
