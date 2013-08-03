// Copyright (c) 2013 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package ldb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/conformal/btcdb"
	"github.com/conformal/btcwire"
)

// InsertBlockData stores a block hash and its associated data block with a
// previous sha of `prevSha' and a version of `pver'.
func (db *LevelDb) InsertBlockData(sha *btcwire.ShaHash, prevSha *btcwire.ShaHash, pver uint32, buf []byte) (blockid int64, err error) {
	db.dbLock.Lock()
	defer db.dbLock.Unlock()

	return db.insertBlockData(sha, prevSha, buf)
}

func (db *LevelDb) getBlkLoc(sha *btcwire.ShaHash) (int64, error) {
	var blkHeight int64

	key := sha.Bytes()

	data, err := db.bShaDb.Get(key, db.ro)

	if err != nil {
		return 0, err
	}

	// deserialize
	dr := bytes.NewBuffer(data)
	err = binary.Read(dr, binary.LittleEndian, &blkHeight)
	if err != nil {
		fmt.Printf("get getBlkLoc len %v\n", len(data))
		err = fmt.Errorf("Db Corrupt 0")
		return 0, err
	}
	return blkHeight, nil
}

func (db *LevelDb) getBlkByHeight(blkHeight int64) (rsha *btcwire.ShaHash, rbuf []byte, err error) {
	var blkVal []byte

	key := int64ToKey(blkHeight)

	blkVal, err = db.bBlkDb.Get(key, db.ro)
	if err != nil {
		return // exists ???
	}

	var sha btcwire.ShaHash

	sha.SetBytes(blkVal[0:32])

	blockdata := make([]byte, len(blkVal[32:]))
	copy(blockdata[:], blkVal[32:])

	return &sha, blockdata, nil
}

func (db *LevelDb) getBlk(sha *btcwire.ShaHash) (rblkHeight int64, rbuf []byte, err error) {
	var blkHeight int64

	blkHeight, err = db.getBlkLoc(sha)
	if err != nil {
		return
	}

	var buf []byte

	_, buf, err = db.getBlkByHeight(blkHeight)
	if err != nil {
		return
	}
	return blkHeight, buf, nil
}

func (db *LevelDb) setBlk(sha *btcwire.ShaHash, blkHeight int64, buf []byte) error {

	// serialize
	var lw bytes.Buffer
	err := binary.Write(&lw, binary.LittleEndian, blkHeight)
	if err != nil {
		err = fmt.Errorf("Write Fail")
		return err
	}
	shaKey := sha.Bytes()

	blkKey := int64ToKey(blkHeight)

	shaB := sha.Bytes()
	blkVal := make([]byte, len(shaB)+len(buf))
	copy(blkVal[0:], shaB)
	copy(blkVal[len(shaB):], buf)

	db.bShaBatch().Put(shaKey, lw.Bytes())

	db.bBlkBatch().Put(blkKey, blkVal)

	return nil
}

// insertSha stores a block hash and its associated data block with a
// previous sha of `prevSha' and a version of `pver'.
// insertSha shall be called with db lock held
func (db *LevelDb) insertBlockData(sha *btcwire.ShaHash, prevSha *btcwire.ShaHash, buf []byte) (blockid int64, err error) {

	oBlkHeight, err := db.getBlkLoc(prevSha)

	if err != nil {
		// check current block count
		// if count != 0  {
		//	err = btcdb.PrevShaMissing
		//	return
		// }
		oBlkHeight = -1
	}

	// TODO(drahn) check curfile filesize, increment curfile if this puts it over
	blkHeight := oBlkHeight + 1

	err = db.setBlk(sha, blkHeight, buf)

	if err != nil {
		return
	}

	// update the last block cache
	db.lastBlkShaCached = true
	db.lastBlkSha = *sha
	db.lastBlkIdx = blkHeight
	db.nextBlock = blkHeight + 1

	return blkHeight, nil
}

// fetchSha returns the datablock and pver for the given ShaHash.
func (db *LevelDb) fetchSha(sha *btcwire.ShaHash) (rbuf []byte,
	rblkHeight int64, err error) {
	var blkHeight int64
	var buf []byte

	blkHeight, buf, err = db.getBlk(sha)
	if err != nil {
		return
	}

	return buf, blkHeight, nil
}

// ExistsSha looks up the given block hash
// returns true if it is present in the database.
func (db *LevelDb) ExistsSha(sha *btcwire.ShaHash) (exists bool) {
	db.dbLock.Lock()
	defer db.dbLock.Unlock()

	// not in cache, try database
	exists = db.blkExistsSha(sha)
	return
}

// blkExistsSha looks up the given block hash
// returns true if it is present in the database.
// CALLED WITH LOCK HELD
func (db *LevelDb) blkExistsSha(sha *btcwire.ShaHash) bool {

	_, err := db.getBlkLoc(sha)

	if err != nil {
		/*
			 should this warn if the failure is something besides does not exist ?
			log.Warnf("blkExistsSha: fail %v", err)
		*/
		return false
	}
	return true
}

// FetchBlockShaByHeight returns a block hash based on its height in the
// block chain.
func (db *LevelDb) FetchBlockShaByHeight(height int64) (sha *btcwire.ShaHash, err error) {
	db.dbLock.Lock()
	defer db.dbLock.Unlock()

	return db.fetchBlockShaByHeight(height)
}

// fetchBlockShaByHeight returns a block hash based on its height in the
// block chain.
func (db *LevelDb) fetchBlockShaByHeight(height int64) (rsha *btcwire.ShaHash, err error) {
	var sha *btcwire.ShaHash
	sha, _, err = db.getBlkByHeight(height)
	if err != nil {
		return
	}

	return sha, nil
}

// FetchHeightRange looks up a range of blocks by the start and ending
// heights.  Fetch is inclusive of the start height and exclusive of the
// ending height. To fetch all hashes from the start height until no
// more are present, use the special id `AllShas'.
func (db *LevelDb) FetchHeightRange(startHeight, endHeight int64) (rshalist []btcwire.ShaHash, err error) {
	db.dbLock.Lock()
	defer db.dbLock.Unlock()

	var endidx int64
	if endHeight == btcdb.AllShas {
		endidx = startHeight + 500
	} else {
		endidx = endHeight
	}

	var shalist []btcwire.ShaHash
	for height := startHeight; height < endidx; height++ {
		// TODO(drahn) fix blkFile from height

		key := int64ToKey(height)
		blkVal, lerr := db.bBlkDb.Get(key, db.ro)
		if lerr != nil {
			break
		}

		var sha btcwire.ShaHash
		sha.SetBytes(blkVal[0:32])
		shalist = append(shalist, sha)
	}

	if err != nil {
		return
	}
	//log.Tracef("FetchIdxRange idx %v %v returned %v shas err %v", startHeight, endHeight, len(shalist), err)

	return shalist, nil
}

// NewestSha returns the hash and block height of the most recent (end) block of
// the block chain.  It will return the zero hash, -1 for the block height, and
// no error (nil) if there are not any blocks in the database yet.
func (db *LevelDb) NewestSha() (rsha *btcwire.ShaHash, rblkid int64, err error) {
	db.dbLock.Lock()
	defer db.dbLock.Unlock()

	if db.lastBlkIdx == -1 {
		err = fmt.Errorf("Empty Database")
		return
	}
	sha := db.lastBlkSha

	return &sha, db.lastBlkIdx, nil
}

func (db *LevelDb) NewIterateBlocks() (rbogus btcdb.BlockIterator, err error) {
	err = fmt.Errorf("Not implemented")
	return
}