package cache

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v9"
)

const (
	// LockScript 加锁的脚本
	LockScript = `if (redis.call('exists', KEYS[1]) == 0) then
redis.call('hincrby', KEYS[1], ARGV[2], 1)
redis.call('pexpire', KEYS[1], ARGV[1])
return "OK"
end

if (redis.call('hexists', KEYS[1], ARGV[2]) == 1) then
redis.call('hincrby', KEYS[1], ARGV[2], 1)
redis.call('pexpire', KEYS[1], ARGV[1])
return "OK"
end
return redis.call('pttl', KEYS[1])
`
	// KeepAliveScript 维持锁的脚本
	KeepAliveScript = `if (redis.call('hexists', KEYS[1], ARGV[2]) == 1) then
redis.call('pexpire', KEYS[1], ARGV[1])
return "OK"
end
return "FIAL"
`

	// UnlockScript 解锁的脚本
	UnlockScript = `if (redis.call('hexists', KEYS[1], ARGV[1]) == 0) then
return "OK"
end
local counter = redis.call('hincrby', KEYS[1], ARGV[1], -1)
if (counter <= 0) then
redis.call('del', KEYS[1])
return "OK"
end
return "reduce"
`
)

// RedisMutex redis 分布式锁
type RedisMutex struct {
	//redis 客户端实例
	Redis *redis.Client
	//锁的存活时间
	TTL time.Duration
	//间隔多久去给锁续期(要比TTL短，不然就没意义了)
	PerKeepAlive time.Duration
	//锁超时时间，避免持有锁的goroutine长时间阻塞（需要做取舍）
	TimeOut time.Duration
	//最大等待锁多久
	MaxWait time.Duration
	//要加锁的key
	Key string
	//作为goroutine的标识
	ID string
	//控制续期任务的关闭
	ctx    context.Context
	cancel context.CancelFunc
}

// RedisLockInstance 获取硕
func RedisLockInstance(ctx context.Context, tag, key, id string) LockerInterface {
	lock := new(RedisMutex)
	lock.Redis = RedisCli
	lock.TTL = 30 * time.Second
	lock.PerKeepAlive = 4 * time.Second
	lock.TimeOut = 3 * time.Second
	lock.MaxWait = 2 * time.Second
	lock.ID = id
	lock.Key = fmt.Sprintf("%s:%s", tag, key)
	return lock
}

// LockerInterface 接口
type LockerInterface interface {
	Lock() error
	Unlock() error
}

// Lock 加锁 如果成功直接返回，如果失败则循环阻塞重试
func (r *RedisMutex) Lock() (err error) {
	res := r.Redis.Eval(r.ctx, LockScript, []string{r.Key}, []string{strconv.Itoa(int(r.TTL.Milliseconds())), r.ID})
	if res.Err() != nil {
		return res.Err()
	}
	val, ok := res.Val().(string)
	if ok && val == "OK" {
		if r.cancel == nil {
			ctx, cancel := context.WithTimeout(r.ctx, r.TimeOut)
			r.cancel = cancel
			go KeepAliveWorker(ctx, r.Redis, r.PerKeepAlive, r.TTL, r.Key, r.ID)
		}
		return
	}
	//没有获取到锁则返回结果是锁的过期时间，重复循环获取
	ttl := time.Duration(res.Val().(int64))
	ctx, _ := context.WithTimeout(r.ctx, r.MaxWait)
	for {
		waiterAfter := time.After(time.Duration(ttl) * time.Millisecond)
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待锁锁超时")
		case <-waiterAfter:
			//尝试获取锁
			res := r.Redis.Eval(r.ctx, LockScript, []string{r.Key}, []string{strconv.Itoa(int(r.TTL.Milliseconds())), r.ID})
			if res.Err() != nil {
				return res.Err()
			}
			val, ok := res.Val().(string)
			if ok && val == "OK" {
				ctx, cancel := context.WithTimeout(r.ctx, r.TimeOut)
				r.cancel = cancel
				go KeepAliveWorker(ctx, r.Redis, r.PerKeepAlive, r.TTL, r.Key, r.ID)
				return nil
			}
			ttl = time.Duration(res.Val().(int64))
		}
	}
}

// Unlock 锁的释放，当key不在了的时候，取消续租任务
func (r *RedisMutex) Unlock() (err error) {
	res := r.Redis.Eval(r.ctx, UnlockScript, []string{r.Key}, []string{r.ID})
	if res.Err() != nil {
		return res.Err()
	}
	val := res.Val().(string)
	//锁不存在或者被删除了
	if val == "OK" && r.cancel != nil {
		//取消续租任务
		r.cancel()
		r.cancel = nil
	}

	return
}

// KeepAliveWorker 续租任务 通过超时和主动取消控制
func KeepAliveWorker(ctx context.Context, redis *redis.Client, PerKeepAlive, ttl time.Duration, key, id string) {
	ticker := time.Tick(PerKeepAlive)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker:
			// 执行续租任务
			res := redis.Eval(ctx, KeepAliveScript, []string{key}, []string{strconv.Itoa(int(ttl.Milliseconds())), id})
			if res.Err() != nil {
				log.Fatal(res.Err().Error())
			}
			if res.Val().(string) != "OK" {
				return
			}
		}
	}
}
