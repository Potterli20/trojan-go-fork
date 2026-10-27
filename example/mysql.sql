-- trojan-go 的 MySQL 用户表（对应 issue #102：表结构、maxip 与 expire 到底是什么）
--
-- 这份文件与代码里的 DDL 逐字同源：statistic/mysql/mysql.go 的 usersTableDDL。
-- 进程启动时 ensureUsersTable 会幂等地「缺表就建、缺列就补」，所以**新建部署不必
-- 手工执行本文件**；它存在的意义是给两类人看的：
--   1. 只想读表结构的面板/运维——这里就是权威定义；
--   2. 账号没有 DDL 权限、需要 DBA 代跑迁移的人——用下面的 ALTER 段。
--
-- 建库建表（MySQL 8 / MariaDB 10.5+）
CREATE TABLE IF NOT EXISTS `users` (
    `username` VARCHAR(64) NOT NULL,
    `password` CHAR(56) NOT NULL,
    `quota` BIGINT NOT NULL DEFAULT -1,
    `download` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `upload` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `maxip` INT NOT NULL DEFAULT 0,
    `expire` BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (`password`)
);

-- 从旧表升级：只补少的列（重复执行会报 Duplicate column name，程序内部靠列探测放行，
-- 手工执行时看到该报错说明这一列已经加过了，可以忽略）
-- ALTER TABLE `users` ADD COLUMN `maxip` INT NOT NULL DEFAULT 0;
-- ALTER TABLE `users` ADD COLUMN `expire` BIGINT NOT NULL DEFAULT 0;

-- 每列的语义
-- username  ：人类可读的名字，只用来在 API/日志里指代用户，不参与认证。
-- password  ：SHA224(客户端口令) 的 56 位小写十六进制，**不是明文口令**。trojan 握手发的
--             就是这串哈希，服务端拿它查表，所以它被选为主键。大小写不符即认证失败。
--             主键要求哈希唯一：已存在的旧表如果 password 有重复行，
--             `UPDATE ... WHERE password=?` 会把同一份流量增量记到多行上，请自行去重。
-- quota     ：累计流量额度，单位字节。语义与 download/upload 直接比较：
--               < 0  → 不限流量；
--               = 0  → 无额度（任何用量都视为用尽，用户会被拒绝认证）；
--               > 0  → 已用 download+upload 达到该值即视为用尽。
--             默认值刻意取 -1：与「配置文件里的内存用户没有额度概念」保持一致，
--             也让只填 (username, password) 的插入语句得到一个能用的用户，
--             而不是被首个同步轮静默删掉。
-- download  ：累计**下行**字节数（用户视角，即服务端发出的量），由进程批量累加。
-- upload    ：累计**上行**字节数（用户视角，即服务端收到的量），由进程批量累加。
--             注意写回时两个方向在代码里是刻意互换的（syncUsers 的原注释
--             "swap upload and download for users"），对着表里的数字排查时先记住这条。
-- maxip     ：该用户的**并发源 IP 上限**，0 = 不限。这是每用户一条的值，
--             与顶层配置里的 max_ip_per_user（对所有内存用户生效）是两条独立路径。
-- expire    ：到期时间，Unix **秒**级时间戳；0 或负数 = 永不过期。
--             到期后认证当场被拒（不需要等下一轮同步），已有连接不受影响——
--             trojan 没有会话中途重新认证。续期直接改这一列即可，下一轮同步生效；
--             到期不等于删除，行会被保留。
--
-- 写入方式：这七列都由你（面板/SQL）写，进程只会**累加** download/upload 两列，
-- 其余列进程只读。用户增减的权威来源就是这张表：表里删一行，下一轮同步该用户即失效。
--
-- 例：加一个不限流量、永不过期、最多 3 个源 IP 的用户
-- INSERT INTO `users` (`username`, `password`, `maxip`)
--     VALUES ('alice', '<56 位 SHA224 十六进制>', 3);
-- 例：给 alice 设 20 GiB 额度、2027-01-01 00:00:00 UTC 到期
-- UPDATE `users` SET `quota` = 21474836480, `expire` = 1798761600 WHERE `username` = 'alice';
-- 例：立刻停掉一个用户（保留行以便日后续期）
-- UPDATE `users` SET `expire` = 1 WHERE `username` = 'alice';
