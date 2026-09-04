package storager

import (
	"os"
	"testing"
)

func TestQiniuUpload(t *testing.T) {
	if os.Getenv("IPA_SERVER_INTEGRATION_TESTS") == "" {
		t.Skip("set IPA_SERVER_INTEGRATION_TESTS=1 to run cloud storage integration tests")
	}
	zone := ""
	accessKeyId := "<yourAccessKeyId>"
	accessKeySecret := "<yourAccessKeySecret>"
	bucketName := "<yourBucketName>"
	domain := "<yourDomain>"

	q, err := NewQiniuStorager(zone, accessKeyId, accessKeySecret, bucketName, domain)
	if err != nil {
		t.Fatal(err)
	}

	testStorager(q, t)
}
