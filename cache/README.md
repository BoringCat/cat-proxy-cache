## 缓存

索引分离式缓存设计，源于
- memcached Key长度限制
- Tendis Scan功能不符合预期


### 索引
(可选) 为数据缓存提供索引功能

用途:
- 手动清理缓存

数据结构
```
${前缀}:${Host}${Path}
├──${cache_key1}
├──${cache_key2}
├──${cache_key3}
└──${cache_key4}
```
> Host: 用户请求服务的Host  
> Path: 用户请求服务的Url路径  
> cache_keyXXX: 配置文件cache_key字段渲染出来的值

### 数据
提供数据缓存

数据结构
```
${cache_key1}: blob
${cache_key2}: blob
${cache_key3}: blob
${cache_key4}: blob
```
