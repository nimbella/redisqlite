package redisqlite

import "C"
import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	// sqlite database driver
	_ "github.com/mattn/go-sqlite3"
)

// database access
var db *sql.DB

// prepared statement cache
const PREP_MAX_SIZE = 10000

var prep_index uint64 = 1
var prep_cache map[uint64]*sql.Stmt

// Open opens the sqlite database
func Open() (err error) {
	db, err = sql.Open("sqlite3", "./sqlite.db")
	prep_cache = make(map[uint64]*sql.Stmt)
	return err
}

// Exec execute a statement applying an array of arguments,
// returns the number of affected rows and the last id modified, when applicable
func Exec(stmtOrNumber string, args []interface{}) (count int64, lastId int64, err error) {
	var res sql.Result
	// select number or string
	index, err := strconv.ParseUint(stmtOrNumber, 10, 64)
	if err == nil {
		stmt := prep_cache[index]
		if stmt == nil {
			return -1, -1, errors.New("no such prepared statement index")
		}
		res, err = stmt.Exec(args...)
		if err != nil {
			return -1, -1, err
		}
	} else {
		res, err = db.Exec(stmtOrNumber, args...)
		if err != nil {
			return -1, -1, err
		}
	}

	count, err = res.RowsAffected()
	if err != nil {
		count = -1
	}
	lastId, err = res.LastInsertId()
	if err != nil {
		lastId = -1
	}
	return lastId, count, nil
}

// Query execute a query applying an array of args
// query can be either an sql string or a number
// if it is a number then it will execute a prepared statement idenfied by the number returned by Prep
// returns an array of results, either as an array of maps or as an array of arrays
// according the `asMap` parameters, and returns up to `count` results (0 for everything)
func Query(queryOrNumber string, args []interface{}, asMap bool, count int64) (res []string, err error) {
	// execute a query
	var rows *sql.Rows

	// grab cached stmp
	index, err := strconv.ParseUint(queryOrNumber, 10, 64)
	if err == nil {
		stmt := prep_cache[index]
		if stmt == nil {
			return nil, errors.New("no such prepared statement index")
		}
		rows, err = stmt.Query(args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
	} else {
		rows, err = db.Query(queryOrNumber, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
	}

	// prepare output
	out := make([]string, 0)
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	ncol := len(columns)
	values := make([]interface{}, ncol)
	scanArgs := make([]interface{}, ncol)
	for i := range values {
		scanArgs[i] = &values[i]
	}
	// scan rows
	for rows.Next() {
		err = rows.Scan(scanArgs...)
		if err != nil {
			return nil, err
		}

		var bytes []byte
		if asMap {
			// serialize as map
			record := make(map[string]interface{})
			for i, v := range values {
				record[columns[i]] = v
			}
			// serialize record
			bytes, err = json.Marshal(record)
			if err != nil {
				continue
			}
		} else {
			// serialize as array
			array := []interface{}{}
			array = append(array, values...)

			// serialize record
			bytes, err = json.Marshal(array)
			if err != nil {
				continue
			}
		}
		// collect
		out = append(out, string(bytes))

		// next until exhausted
		count--
		if count == 0 {
			break
		}
	}
	return out, rows.Err()
}

// Prep accepts prepares a sql statement and stores it in a table
// returning a number. It also accepts a number, and if it corresponds
// to the number returned by a previous statement, it closes the prepared statement
// you can store up to one 10000 statements, if you go over the limit it will return an error
// using the special statement "clean_prep_cache" you can close all the opened statement
// returnend 0 means OK, any other number is the index in the cache
func Prep(queryOrNumber string) (uint64, error) {

	if queryOrNumber == "clean_prep_cache" {
		for key, value := range prep_cache {
			value.Close()
			delete(prep_cache, key)
		}
		return 0, nil
	}

	index, err := strconv.ParseUint(queryOrNumber, 10, 64)
	if err == nil {
		// clean all
		stat, ok := prep_cache[index]
		if ok {
			stat.Close()
			delete(prep_cache, index)
			return 0, nil
		}
		return 0, errors.New("invalid prepared statement index")
	}

	if len(prep_cache) >= PREP_MAX_SIZE {
		return 0, errors.New("too many prepared statements, use clean_prep_cache on prep to clean")
	}

	// get next index and close very old statements if still unclosed
	prep_index = prep_index + 1
	// handle unlikely case of overflow
	if prep_index == 0 {
		prep_index = 1
	}
	stmt, err := db.Prepare(queryOrNumber)
	if err != nil {
		return 0, err
	}
	prep_cache[prep_index] = stmt
	return prep_index, nil
}
