package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"github.com/joho/godotenv"
	"github.com/klauspost/compress/zstd"
)

type manifest struct {
	ChunkSize int `json:"chunk_size"`
	Total     int `json:"total"`
}

type message struct {
	Id      int
	Type    byte // 0:ic 1:radio 2:dchat
	Round   int
	Name    string
	CKey    string
	RadioCh string
	Text    string
	Image   []byte
}

func main() {
	log.Println("Starting superpoly...")
	log.Printf("disgo version %v\n", disgo.Version)

	var msgId int
	var msgChunk int
	{
		maniFile, fileErr := os.ReadFile("/2tb/yogout-backup/manifest.json")
		if fileErr != nil {
			panic(fileErr)
		}
		var data manifest
		jsonErr := json.Unmarshal(maniFile, &data)
		if jsonErr != nil {
			panic(jsonErr)
		}

		msgId = rand.Intn(data.Total)
		msgChunk = msgId / data.ChunkSize
	}

	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file")
	}

	client, clientErr := disgo.New(os.Getenv("disgo_token"),
		// set gateway options
		bot.WithGatewayConfigOpts(
			// set enabled intents
			// --needs no intents, only posts a message once a day to a channel
			gateway.WithIntents(),
		),
		// add event listeners
		bot.WithEventListenerFunc(func(e *events.MessageCreate) {
			// event code here
		}),
	)
	if clientErr != nil {
		log.Fatalf("clientErr: %v", clientErr)
	}
	defer client.Close(context.TODO())

	if gatewayErr := client.OpenGateway(context.TODO()); gatewayErr != nil {
		log.Fatalf("gatewayErr: %v", gatewayErr)
	}

	msgErr, msg := getMsg(msgId, msgChunk)
	if msgErr != nil {
		log.Fatalf("msgErr: %v", msgErr)
	}

	_, discordErr := client.Rest.CreateMessage(snowflake.MustParse("1467124145547509821"), discord.NewMessageCreate().WithContent("").AddFile(fmt.Sprintf("%v.webp", msg.Id), "9x9 capture of the speaker", bytes.NewReader(msg.Image)))
	if discordErr != nil {
		log.Printf("discordErr: %v", discordErr) // dont panick just try again in 24h
	}
	// time.Sleep(24 * time.Hour)
}

func getMsg(msgId int, msgChunk int) (error, *message) {
	indexFile, indexErr := os.Open("/2tb/yogout-backup/index.bin")
	if indexErr != nil {
		return indexErr, nil
	}
	defer indexFile.Close()

	_, seekErr := indexFile.Seek(int64(msgId*20), 0)
	if seekErr != nil {
		return seekErr, nil
	}

	imgOffsetByte := make([]byte, 8)
	_, imgOffsetReadErr := indexFile.Read(imgOffsetByte)
	if imgOffsetReadErr != nil && !errors.Is(imgOffsetReadErr, io.EOF) {
		return imgOffsetReadErr, nil
	}
	imgOffset := binary.LittleEndian.Uint64(imgOffsetByte)

	imgLenOffsetByte := make([]byte, 4)
	_, imgLenOffsetReadErr := indexFile.Read(imgLenOffsetByte)
	if imgLenOffsetReadErr != nil && !errors.Is(imgLenOffsetReadErr, io.EOF) {
		return imgLenOffsetReadErr, nil
	}
	imgLen := binary.LittleEndian.Uint32(imgLenOffsetByte)

	var imgData []byte
	if imgLen > 0 {
		imgData = make([]byte, imgLen)
		imgFile, imgErr := os.Open("/2tb/yogout-backup/img.bin")
		if imgErr != nil {
			return imgErr, nil
		}
		defer imgFile.Close()

		_, imgSeekErr := imgFile.Seek(int64(imgOffset), 0)
		if imgSeekErr != nil {
			return imgSeekErr, nil
		}
		_, imgReadErr := imgFile.Read(imgData)
		if imgReadErr != nil && !errors.Is(imgReadErr, io.EOF) {
			return imgReadErr, nil
		}
	}

	msgOffsetByte := make([]byte, 4)
	_, msgOffsetReadErr := indexFile.Read(msgOffsetByte)
	if msgOffsetReadErr != nil && !errors.Is(msgOffsetReadErr, io.EOF) {
		return msgOffsetReadErr, nil
	}
	msgOffset := binary.LittleEndian.Uint32(msgOffsetByte)

	msgLenOffsetByte := make([]byte, 4)
	_, msgLenOffsetReadErr := indexFile.Read(msgLenOffsetByte)
	if msgLenOffsetReadErr != nil && !errors.Is(msgLenOffsetReadErr, io.EOF) {
		return msgLenOffsetReadErr, nil
	}
	msgLen := binary.LittleEndian.Uint32(msgLenOffsetByte)

	chunkFile, chunkErr := os.Open(fmt.Sprintf("/2tb/yogout-backup/meta/%05d.zst", msgChunk))
	if chunkErr != nil {
		return chunkErr, nil
	}

	chunkDecompressedLenByte := make([]byte, 4)
	_, chunkDecompressedLenReadErr := chunkFile.Read(chunkDecompressedLenByte)
	if chunkDecompressedLenReadErr != nil && !errors.Is(chunkDecompressedLenReadErr, io.EOF) {
		return chunkDecompressedLenReadErr, nil
	}
	chunkDecompressedLen := binary.LittleEndian.Uint32(chunkDecompressedLenByte)

	chunkStat, chunkStatErr := chunkFile.Stat()
	if chunkStatErr != nil {
		return chunkStatErr, nil
	}

	chunkCompressedByte := make([]byte, chunkStat.Size()-4)
	_, chunkCompressedReadErr := chunkFile.Read(chunkCompressedByte)
	if chunkCompressedReadErr != nil && !errors.Is(chunkCompressedReadErr, io.EOF) {
		return chunkCompressedReadErr, nil
	}

	chunkData := make([]byte, 0, chunkDecompressedLen)
	decoder, _ := zstd.NewReader(nil, zstd.WithDecodeAllCapLimit(true))
	chunkData, chunkDataErr := decoder.DecodeAll(chunkCompressedByte, chunkData)
	if chunkDataErr != nil {
		return chunkDataErr, nil
	}
	defer decoder.Close()

	msgRecord := chunkData[msgOffset : msgOffset+msgLen]
	msgFlags := msgRecord[0]
	msgType := (msgFlags >> 2) & 3 // 0:ic 1:radio 2:dchat
	msgRecordOffset := 1
	msgRound, msgRecordOffset := varInt(msgRecord, msgRecordOffset)
	var msgRadioCh string
	if msgFlags&16 == 16 {
		msgRadioCh, msgRecordOffset = varString(msgRecord, msgRecordOffset)
	}
	var msgCKey string
	if msgFlags&1 == 1 {
		msgCKey, msgRecordOffset = varString(msgRecord, msgRecordOffset)
	}
	var msgName string
	if msgFlags&2 == 2 {
		msgName, msgRecordOffset = varString(msgRecord, msgRecordOffset)
	}
	msgText, _ := varString(msgRecord, msgRecordOffset)

	outMsg := new(message)
	outMsg.Id = msgId
	outMsg.Type = msgType
	outMsg.Round = msgRound
	outMsg.Name = msgName
	outMsg.CKey = msgCKey
	outMsg.RadioCh = msgRadioCh
	outMsg.Text = msgText
	outMsg.Image = imgData
	return nil, outMsg
}

func varInt(msgRecord []byte, msgRecordOffset int) (int, int) {
	var out int = 0
	var shift byte = 0
	for {
		b := msgRecord[msgRecordOffset]
		msgRecordOffset++
		out |= (int(b) & 0x7F) << int(shift)
		if b < 0x80 {
			return out, msgRecordOffset
		}
		shift += 7
	}
}

func varString(msgRecord []byte, msgRecordOffset int) (string, int) {
	len, msgRecordOffset := varInt(msgRecord, msgRecordOffset)
	return string(msgRecord[msgRecordOffset : msgRecordOffset+len]), msgRecordOffset + len
}
