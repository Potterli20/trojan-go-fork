package control

import (
	"context"
	"flag"
	"testing"

	"github.com/Potterli20/trojan-go-fork/api/service"
)

// 用显式设置的 flag 构造 controller：只有名字出现在 names 里的 flag 才算"用户显式给出"。
// 真实 Handle 里这张表由 flag.Visit 填充，这里直接注入以免依赖全局 flag.CommandLine。
func controllerWithFlags(t *testing.T, names ...string) *apiController {
	t.Helper()
	fs := flag.NewFlagSet(t.Name(), flag.ContinueOnError)
	cmd := fs.String("api", "set", "")
	addr := fs.String("api-addr", "127.0.0.1:10000", "")
	token := fs.String("api-token", "", "")
	password := fs.String("target-password", "p", "")
	hash := fs.String("target-hash", "", "")
	add := fs.Bool("add-profile", false, "")
	del := fs.Bool("delete-profile", false, "")
	modify := fs.Bool("modify-profile", true, "")
	ul := fs.Int("upload-speed-limit", 0, "")
	dl := fs.Int("download-speed-limit", 0, "")
	ip := fs.Int("ip-limit", 0, "")
	explicit := make(map[string]bool)
	for _, n := range names {
		explicit[n] = true
	}
	return &apiController{
		cmd: cmd, address: addr, token: token, password: password, hash: hash,
		add: add, delete: del, modify: modify,
		uploadSpeedLimit: ul, downloadSpeedLimit: dl, ipLimit: ip,
		explicitFlags: explicit,
		ctx:           context.Background(),
	}
}

func TestModifyOmitsUnsetSpeedAndIPLimit(t *testing.T) {
	// 只给 --modify-profile，不带任何限速/IP flag：构造的请求里 SpeedLimit 与
	// IpLimit 都必须为 nil，否则服务端会把已有配置清零（SpeedLimit 是 message、
	// 非 nil 即触发清零；IpLimit 是 optional）。
	o := controllerWithFlags(t)
	req, err := o.buildSetUsersRequest()
	if err != nil {
		t.Fatalf("buildSetUsersRequest: %v", err)
	}
	if req.Status.SpeedLimit != nil {
		t.Fatalf("未给限速 flag 却构造了 SpeedLimit=%+v：Modify 会把用户限速静默清零", req.Status.SpeedLimit)
	}
	if req.Status.IpLimit != nil {
		t.Fatalf("未给 ip-limit flag 却构造了 IpLimit=%v：Modify 会把用户 IP 上限静默清零", *req.Status.IpLimit)
	}
}

func TestModifySendsExplicitSpeedAndIPLimit(t *testing.T) {
	// 显式给出的 flag 必须照常发送（正向对照：别把清零能力一起删掉）。
	o := controllerWithFlags(t, "upload-speed-limit", "download-speed-limit", "ip-limit")
	*o.uploadSpeedLimit = 100
	*o.downloadSpeedLimit = 200
	*o.ipLimit = 3
	req, err := o.buildSetUsersRequest()
	if err != nil {
		t.Fatalf("buildSetUsersRequest: %v", err)
	}
	if req.Status.SpeedLimit == nil || req.Status.SpeedLimit.UploadSpeed != 100 || req.Status.SpeedLimit.DownloadSpeed != 200 {
		t.Fatalf("显式限速没有发出：%+v", req.Status.SpeedLimit)
	}
	if req.Status.IpLimit == nil || *req.Status.IpLimit != 3 {
		t.Fatalf("显式 ip-limit 没有发出：%v", req.Status.IpLimit)
	}
}

func TestAddSendsDefaultsEvenWhenFlagsOmitted(t *testing.T) {
	// Add 是新建用户：即使没给任何 flag，也要发显式的 0（不限）——新建没有
	// "保留旧值"的语义，这与 Modify 省略时必须 nil 相反。
	o := controllerWithFlags(t)
	*o.add = true
	*o.modify = false
	req, err := o.buildSetUsersRequest()
	if err != nil {
		t.Fatalf("buildSetUsersRequest: %v", err)
	}
	if req.Operation != service.SetUsersRequest_Add {
		t.Fatalf("操作类型不对：%v", req.Operation)
	}
	if req.Status.SpeedLimit == nil || req.Status.IpLimit == nil {
		t.Fatal("Add 应当发送显式的默认限速/IP（0=不限），省略语义只属于 Modify")
	}
}
