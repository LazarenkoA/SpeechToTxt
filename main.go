package main

import (
	"fmt"
	stt "github.com/LazarenkoA/SpeechToTxt/STT"
	"os"
)

var (
	key       = "" // статический ключ доступа
	apikey    = "" // API key
	ID_apikey = ""
	bucket    = ""
)

func init() {
	key = os.Getenv("KEY")
	apikey = os.Getenv("APIKEY")
	bucket = os.Getenv("BUCKET")
	ID_apikey = os.Getenv("IDAPIKEY")
}

func main() {
	sst := new(stt.STT).New(&stt.STTConf{
		Key:       key,
		ID_apikey: ID_apikey,
		Apikey:    apikey,
		Bucket:    bucket,
	})

	out := make(chan string, 1)
	if err := sst.UploadStorageYandexcloud("C:/GoProject/telegramScheduleSendMsg/tmp.ogg"); err == nil {
		if err = sst.SpeechKit(out); err != nil {
			close(out)
			fmt.Println(err)
		}
	} else {
		fmt.Println(err)
		close(out)
	}
	fmt.Println(<-out)
}
