package storager

import (
	"os"
	"testing"
)

func TestAliOss(t *testing.T) {
	if os.Getenv("IPA_SERVER_INTEGRATION_TESTS") == "" {
		t.Skip("set IPA_SERVER_INTEGRATION_TESTS=1 to run cloud storage integration tests")
	}

	endpoint := "oss-cn-shenzhen.aliyuncs.com"
	accessKeyId := "<yourAccessKeyId>"
	accessKeySecret := "<yourAccessKeySecret>"
	bucketName := "<yourBucketName>"
	domain := "<yourDomain>"

	a, err := NewAliOssStorager(endpoint, accessKeyId, accessKeySecret, bucketName, domain)
	if err != nil {
		t.Fatal(err)
	}

	testStorager(a, t)
}
