package main

//1. reading from mdx file
//2. parser and validate the mdx
//3. indexing into sqlite3 db
//4. add a http handler func accept word query
// header: 字典的基本信息，重要字段： 名称 生成引擎版本 是否加密
// keyBlock: 类似字典的词条索引，只有词条信息，没有释义
// recordBlock: 类似字典的词条+释义

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"golang.org/x/text/encoding/unicode"
	"hash/adler32"
	"io"
	"os"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}
func main() {

	f, err := os.Open("/Users/zhimoe/code/mdict-py/resources/mdx/en/牛津高阶8.mdx")
	defer f.Close()
	check(err)

	headerLenBuf := make([]byte, 4)
	_, err = f.Read(headerLenBuf)
	check(err)
	headerLen := binary.BigEndian.Uint32(headerLenBuf)
	fmt.Printf("the header len: %d\n", headerLen)

	headerBuf := make([]byte, headerLen)
	_, err = f.Read(headerBuf)
	check(err)

	adler32Buf := make([]byte, 4)
	_, err = f.Read(adler32Buf)
	check(err)
	adler32Uint := binary.LittleEndian.Uint32(adler32Buf)
	sum := adler32.Checksum(headerBuf)
	if sum&0xffffffff != adler32Uint {
		panic("can't read the mdx file, cuz the header adler32 checksum failed")
	}

	keyBlockOffset, err := f.Seek(0, io.SeekCurrent)
	fmt.Printf("the keyBlockOffset position is %d \n", keyBlockOffset)

	//	extract header info
	headerStr, _ := DecodeUTF16(headerBuf)
	fmt.Printf("the header string is %s\n", headerStr)

	var EngineVersion = 2
	var numberWidth, metaByteSize = 8, 8 * 5
	if EngineVersion < 2 {
		numberWidth = 4
		metaByteSize = 4 * 4
	}
	keyBlockMetaBuf := make([]byte, metaByteSize)
	_, err = f.Read(keyBlockMetaBuf)
	check(err)

	numOfKeyBlocks := binary.BigEndian.Uint64(keyBlockMetaBuf[0:numberWidth])
	fmt.Printf("the numOfKeyBlocks is %d \n", numOfKeyBlocks)
	numOfEntries := binary.BigEndian.Uint64(keyBlockMetaBuf[numberWidth : numberWidth*2])
	fmt.Printf("the numOfEntries is %d \n", numOfEntries)

	keyBlockAdler32 := binary.BigEndian.Uint64(keyBlockMetaBuf[numberWidth*2 : numberWidth*3])
	fmt.Printf("the keyBlockAdler32 is %d \n", keyBlockAdler32)

	keyBlockInfoBytesLen := binary.BigEndian.Uint64(keyBlockMetaBuf[numberWidth*3 : numberWidth*4])
	fmt.Printf("the keyBlockInfoLen is %d \n", keyBlockInfoBytesLen)

	keyBlocksBytesLen := binary.BigEndian.Uint64(keyBlockMetaBuf[numberWidth*4 : numberWidth*5])
	fmt.Printf("the keyBlocksBytesLen is %d \n", keyBlocksBytesLen)

	keyBlockMetaAdler32Buf := make([]byte, 4)
	_, err = f.Read(keyBlockMetaAdler32Buf)
	check(err)
	keyBlockMetaAdler32Uint := binary.BigEndian.Uint32(keyBlockMetaAdler32Buf)

	if adler32.Checksum(keyBlockMetaBuf)&0xffffffff != keyBlockMetaAdler32Uint {
		panic("keyBlockMeta bytes adler32 checksum failed")
	}

	keyBlockInfoBuf := make([]byte, keyBlockInfoBytesLen)
	_, err = f.Read(keyBlockInfoBuf)
	check(err)

	keyBlocksBuf := make([]byte, keyBlocksBytesLen)
	_, err = f.Read(keyBlocksBuf)
	check(err)

	recordBlockOffset, err := f.Seek(0, io.SeekCurrent)
	fmt.Printf("the recordBlockOffset position is %d \n", recordBlockOffset)

	//	keyBlockInfoBuf 解析key block info bytes
	var keyBlockInfoList = DecodeKeyBlockInfo(keyBlockInfoBuf)
	fmt.Printf("the keyBlockInfoList is %s \n", keyBlockInfoList)
	var keyIdTextList = DecodeKeyBlock(keyBlocksBuf, keyBlockInfoList)
	fmt.Printf("the keyIdTextList is %s", keyIdTextList)
}

type KeyBlockItemSize struct {
	KbCompressedSize   uint64
	KbDecompressedSize uint64
}

func (kb KeyBlockItemSize) String() string {
	return fmt.Sprintf("{cs: %d, ds: %d}", kb.KbCompressedSize, kb.KbDecompressedSize)
}

func DecodeKeyBlockInfo(keyBlockInfoBytes []byte) []KeyBlockItemSize {
	var ret = make([]KeyBlockItemSize, 0)
	var byteWidth uint64 = 2
	var textTerm uint64 = 1
	var numWidth uint64 = 8
	z, err := zlib.NewReader(bytes.NewReader(keyBlockInfoBytes[8:]))
	if err != nil {
		fmt.Println("zlib解压错误", err)
	}
	defer z.Close()
	decompressed, _ := io.ReadAll(z)

	var i uint64 = 0
	var numEntries uint64 = 0
	for i < uint64(len(decompressed)) {
		numEntries += binary.BigEndian.Uint64(decompressed[i : i+numWidth])
		i += numWidth
		textHeadSize := binary.BigEndian.Uint16(decompressed[i : i+byteWidth])
		i += byteWidth
		i += uint64(textHeadSize) + textTerm
		textTailSize := binary.BigEndian.Uint16(decompressed[i : i+byteWidth])
		i += byteWidth
		i += uint64(textTailSize) + textTerm

		compressedSize := binary.BigEndian.Uint64(decompressed[i : i+numWidth])
		i += numWidth
		decompressedSize := binary.BigEndian.Uint64(decompressed[i : i+numWidth])
		i += numWidth
		ret = append(ret, KeyBlockItemSize{compressedSize, decompressedSize})
	}
	return ret
}

type KeyIdText struct {
	id   uint64
	text string
}

func (it KeyIdText) String() string {
	return fmt.Sprintf("KeyIdText{id: %d, text: %s}", it.id, it.text)
}

func DecodeKeyBlock(keyBlocksBytes []byte, keyBlockItemSizeList []KeyBlockItemSize) []KeyIdText {
	var i = 0
	var end = uint64(i)
	var ret = make([]KeyIdText, 0)
	numWidth := 8

	for _, v := range keyBlockItemSizeList {
		compressedSize, _ := v.KbCompressedSize, v.KbDecompressedSize
		start := i
		end += compressedSize
		oneBlockBytes := keyBlocksBytes[start:end]

		z, err := zlib.NewReader(bytes.NewReader(oneBlockBytes[8:]))
		if err != nil {
			fmt.Println("zlib解压错误", err)
		}
		defer z.Close()
		decodedBytes, _ := io.ReadAll(z)
		keyStart := 0
		keyEnd := 0
		var delimiter []byte = []byte{0x00}
		delimiterWidth := 1
		for keyStart < len(decodedBytes) {
			id := binary.BigEndian.Uint64(decodedBytes[keyStart : keyStart+numWidth])
			textStart := keyStart + numWidth
			temp := textStart
			for temp < len(decodedBytes) {
				if bytes.Compare(decodedBytes[temp:temp+delimiterWidth], delimiter) == 0 {
					keyEnd = temp
					break
				}
				temp += delimiterWidth
			}
			text := string(decodedBytes[textStart:keyEnd])
			keyStart += keyEnd + delimiterWidth
			ret = append(ret, KeyIdText{
				id,
				text,
			})
		}
	}

	return ret
}

func DecodeUTF16(buf []byte) (string, error) {
	if len(buf)%2 != 0 {
		return "", fmt.Errorf("must have even length byte slice")
	}
	enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	decoder := enc.NewDecoder()
	utf8, _ := decoder.Bytes(buf[2:])
	return string(utf8), nil
}
