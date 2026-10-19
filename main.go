package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strings"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
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

var discordText = strings.NewReplacer(
	`*`, `\*`, // italic bold
	`_`, `\_`, // italic underline
	`~`, `\~`, // strikeout
	`|`, `\|`, // spoiler
	"`", "\\`", // code blocks
	`@`, `\@`, // ping
	`#`, `\#`, // channels
	`<`, `\<`, // hide links
	`>`, `\>`, // hide links
	`\`, `\\`, // escape escape
)

func main() {
	log.Println("Starting superpoly...")
	log.Printf("disgo version %v\n", disgo.Version)

	var total int
	var chunks int
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

		total = data.Total
		chunks = data.ChunkSize
	}

	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file")
	}

	{
		msgId = rand.Intn(total)
		msgChunk = msgId / chunks

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
			bot.WithRestConfigOpts(
				rest.WithDefaultAllowedMentions(discord.AllowedMentions{
					Parse: []discord.AllowedMentionType{},
				}),
			),
		)
		if clientErr != nil {
			log.Fatalf("clientErr: %v", clientErr)
		}
		defer client.Close(context.TODO())

		msgErr, msg := getMsg(msgId, msgChunk)
		if msgErr != nil {
			log.Fatalf("msgErr: %v", msgErr)
		}

		textRadio := ""
		if msg.Type == 1 {
			textRadio = fmt.Sprintf("[%v] ", msg.RadioCh)
		}

		textCKey := ""
		if msg.Type != 2 {
			textCKey = fmt.Sprintf("(%v) ", msg.CKey)
		}

		// this code is supremely ugly
		_, discordErr := client.Rest.CreateMessage(snowflake.MustParse("1467124145547509821"), discord.NewMessageCreate().WithContent(
			// text
			fmt.Sprintf("-# Round %v\n%v", msg.Round, discordText.Replace(fmt.Sprintf("%v%v%v says, \"%v\"\n", textRadio, textCKey, msg.Name, msg.Text)))).AddFile(

			// image
			fmt.Sprintf("%v.webp", msg.Id), "", bytes.NewReader(msg.Image)))

		if discordErr != nil {
			log.Printf("discordErr: %v", discordErr) // dont panick just try again in 24h
		}
	}
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
	_, imgOffsetReadErr := io.ReadFull(indexFile, imgOffsetByte)
	if imgOffsetReadErr != nil {
		return imgOffsetReadErr, nil
	}
	imgOffset := binary.LittleEndian.Uint64(imgOffsetByte)

	imgLenOffsetByte := make([]byte, 4)
	_, imgLenOffsetReadErr := io.ReadFull(indexFile, imgLenOffsetByte)
	if imgLenOffsetReadErr != nil {
		return imgLenOffsetReadErr, nil
	}
	imgLen := binary.LittleEndian.Uint32(imgLenOffsetByte)

	var imgData []byte
	if imgLen > 0 && imgLen < 100000 { // images are ~13kB
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
		_, imgReadErr := io.ReadFull(imgFile, imgData)
		if imgReadErr != nil {
			return imgReadErr, nil
		}
	}

	msgOffsetByte := make([]byte, 4)
	_, msgOffsetReadErr := io.ReadFull(indexFile, msgOffsetByte)
	if msgOffsetReadErr != nil {
		return msgOffsetReadErr, nil
	}
	msgOffset := binary.LittleEndian.Uint32(msgOffsetByte)

	msgLenOffsetByte := make([]byte, 4)
	_, msgLenOffsetReadErr := io.ReadFull(indexFile, msgLenOffsetByte)
	if msgLenOffsetReadErr != nil {
		return msgLenOffsetReadErr, nil
	}
	msgLen := binary.LittleEndian.Uint32(msgLenOffsetByte)

	chunkFile, chunkErr := os.Open(fmt.Sprintf("/2tb/yogout-backup/meta/%05d.zst", msgChunk))
	if chunkErr != nil {
		return chunkErr, nil
	}
	defer chunkFile.Close()

	chunkDecompressedLenByte := make([]byte, 4)
	_, chunkDecompressedLenReadErr := io.ReadFull(chunkFile, chunkDecompressedLenByte)
	if chunkDecompressedLenReadErr != nil {
		return chunkDecompressedLenReadErr, nil
	}
	chunkDecompressedLen := binary.LittleEndian.Uint32(chunkDecompressedLenByte)

	chunkStat, chunkStatErr := chunkFile.Stat()
	if chunkStatErr != nil {
		return chunkStatErr, nil
	}

	chunkCompressedByte := make([]byte, chunkStat.Size()-4)
	_, chunkCompressedReadErr := io.ReadFull(chunkFile, chunkCompressedByte)
	if chunkCompressedReadErr != nil {
		return chunkCompressedReadErr, nil
	}

	chunkData := make([]byte, 0, chunkDecompressedLen)
	decoder, zstdErr := zstd.NewReader(nil, zstd.WithDecodeAllCapLimit(true))
	if zstdErr != nil {
		return zstdErr, nil
	}
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
