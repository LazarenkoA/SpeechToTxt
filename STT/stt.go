package STT

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

type STTConf struct {
	// статический ключ доступа
	Key string

	// Идентификатор статического ключ доступа
	ID_apikey string

	// API key
	Apikey string

	Bucket string
}

type STT struct {
	conf   *STTConf
	oggKey string
	s3     s3iface.S3API
}

const (
	endpoint = "https://storage.yandexcloud.net"
)

func (s *STT) New(conf *STTConf) *STT {
	s.conf = conf
	if conf.Apikey == "" {
		panic("не заполнен Apikey")
	}
	if conf.Key == "" {
		panic("не заполнен Key")
	}
	if conf.ID_apikey == "" {
		panic("не заполнен ID_apikey")
	}
	if conf.Bucket == "" {
		panic("не заполнен Bucket")
	}

	return s
}

func (s *STT) UploadStorageYandexcloud(filePath string) error {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return errors.Wrap(err, "ошибка чтения файла")
	}

	_, filename := splitPath(filePath)
	key := fmt.Sprintf("%s-%s", uuid.New().String(), filename)

	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(s.conf.ID_apikey, s.conf.Key, ""),
		Endpoint:    aws.String(endpoint),
		Region:      aws.String("ru-central1"),
	},
	)

	uploader := s3manager.NewUploader(sess)
	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.conf.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	})
	if err != nil {
		err := fmt.Errorf("unable to upload %q to %q, %v", filename, s.conf.Bucket, err)
		return err
	}

	s.s3 = uploader.S3
	// пример получения списка файлов
	//tr, err := uploader.S3.ListObjects(&s3.ListObjectsInput{
	//	Bucket: aws.String(bucket),
	//})
	//if err == nil {
	//	for _, o := range tr.Contents {
	//		object, err := uploader.S3.GetObject(&s3.GetObjectInput{
	//			Bucket: aws.String(bucket),
	//			Key:    o.Key,
	//		})
	//
	//		fmt.Println(object, err)
	//	}
	//}

	s.oggKey = key
	return nil
}

func (s *STT) SpeechKit(out chan string) error {
	data := map[string]interface{}{
		"config": map[string]interface{}{
			"specification": map[string]interface{}{
				"languageCode": "ru-RU",
			},
		},
		"audio": map[string]interface{}{
			"uri": fmt.Sprintf("%s/%s/%s", endpoint, s.conf.Bucket, s.oggKey),
		},
	}

	b, err := json.Marshal(&data)
	if err != nil {
		return err
	}
	resp, err := s.post("https://transcribe.api.cloud.yandex.net/speech/stt/v2/longRunningRecognize", b)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return fmt.Errorf("StatusCode: %d\n"+
			"Body: %s\n", resp.StatusCode, string(respBody))
	}

	request, err := s.restToJSON(respBody)
	if err != nil {
		return err
	}

	go s.observe(request["id"].(string), out)

	return nil
}

func (s *STT) deleteFile() {
	s.s3.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.conf.Bucket),
		Key:    aws.String(s.oggKey),
	})
}

func (s *STT) observe(operationID string, out chan string) {
	t := time.NewTicker(time.Millisecond * 500)
	defer t.Stop()
	defer close(out)

	timeout := time.After(time.Minute)
FOR:
	for {
		select {
		case <-timeout:
			out <- "прервано по таймауту"
			break FOR
		case <-t.C:
			resp, err := s.get(fmt.Sprintf("https://operation.api.cloud.yandex.net/operations/%s", operationID))
			if err != nil {
				fmt.Println(err)
				break FOR
			}

			respBody, _ := ioutil.ReadAll(resp.Body)
			resp.Body.Close()

			request, err := s.restToJSON(respBody)
			if err != nil {
				fmt.Println("ошибка сериализации json: ", err)
				break FOR
			}
			if done, _ := request["done"].(bool); done {
				func() {
					// EAFP
					// что б упростить работу с пустым интерфейсом
					defer func() {
						if err := recover(); err != nil {
							fmt.Println("произошла ошибка: ", err)
						}
					}()

					result := []string{}
					for _, chunk := range request["response"].(map[string]interface{})["chunks"].([]interface{}) {
						for _, alternative := range chunk.(map[string]interface{})["alternatives"].([]interface{}) {
							result = append(result, alternative.(map[string]interface{})["text"].(string))
						}
					}

					s.deleteFile()
					out <- strings.Join(result, ". ")
				}()
				break FOR
			}
		}
	}

}

func (s *STT) restToJSON(b []byte) (map[string]interface{}, error) {
	request := map[string]interface{}{}
	err := json.Unmarshal(b, &request)
	if err != nil {
		return request, errors.Wrap(err, "ошибка десириализации json")
	}

	return request, nil
}

func (s *STT) post(url string, body []byte) (*http.Response, error) {
	client := &http.Client{Timeout: time.Second * 30}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Api-Key %s", s.conf.Apikey))

	if resp, err := client.Do(req); err != nil {
		return nil, err
	} else {
		return resp, nil
	}
}

func (s *STT) get(url string) (*http.Response, error) {
	client := &http.Client{Timeout: time.Second * 30}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Api-Key %s", s.conf.Apikey))

	if resp, err := client.Do(req); err != nil {
		return nil, err
	} else {
		return resp, nil
	}
}

func splitPath(path string) (dir, file string) {
	i := strings.LastIndex(path, string(os.PathSeparator))
	return path[:i+1], path[i+1:]
}
