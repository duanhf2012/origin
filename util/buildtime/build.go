package buildtime

/*
//查询buildtime包中的位置，在github.com/duanhf2012/origin/util/buildtime.BuildTime中
go tool nm ./originserver.exe |grep buildtime

//编译传入编译时间信息
go build -ldflags "-X 'github.com/duanhf2012/origin/util/buildtime.BuildTime=20200101'"
go build -ldflags "-X github.com/duanhf2012/origin/util/buildtime.BuildTime=20200101 -X github.com/duanhf2012/origin/util/buildtime.BuildTag=debug"
*/
var BuildTime string
var BuildTag  string

func GetBuildDateTime() string {
	return BuildTime
}

func GetBuildTag() string {
	return BuildTag
}
