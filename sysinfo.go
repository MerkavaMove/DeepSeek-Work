package main

// 电脑信息采集（v1.0.5）——行式模型：
//
//	SysInfo{ Rows: []InfoRow{ 处理器, 主板, 内存, 显卡, 显示器, 磁盘, 系统 } }
//
// 每行独立采集、独立降级：某一项取不到就显示「—」，不影响其他行。
// 多值场景（多显卡/多硬盘/多显示器）用行内 Subs 子行展示，
// 子行可带自己的小标签（集成显卡/独立显卡/主硬盘/显示器1…）。
//
// 数据来源（全部 Windows 通用，不需要管理员权限）：
//   - WMI：处理器/主板/内存条/显卡/显示器/硬盘/系统分区
//   - nvidia-smi：NVIDIA 卡的显存（WMI 的 AdapterRAM 在新卡上经常不准）
//   - gopsutil host：系统版本
//
// 所有 WMI 查询都可能因精简系统镜像缺类/缺字段而失败——每处都做了
// 容错（返回「—」或省略该细节），保证"兼容各种电脑"。

import (
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/yusufpapurcu/wmi"
)

// SysInfo 电脑信息：按行组织，行序即显示顺序。
type SysInfo struct {
	Rows []InfoRow `json:"rows"`
}

// InfoRow 一行信息：Label 行标签；Value 单行值；Subs 子行（多值场景）。
// Value 为空且 Subs 非空时，前端只显示 Section 标签 + 子行。
type InfoRow struct {
	Label string    `json:"label"`
	Value string    `json:"value"`
	Subs  []InfoRow `json:"subs,omitempty"`
}

// gpuEntry 一张显卡（kind：集成显卡/独立显卡）。
type gpuEntry struct {
	kind   string
	name   string
	vram   string
	vendor string
}

// 行序与目标展示一致：处理器/主板/内存/显卡/显示器/磁盘/系统。
func CollectSysInfo() SysInfo {
	return SysInfo{Rows: []InfoRow{
		rowCPU(),
		rowBoard(),
		rowMem(),
		rowGPU(),
		rowMonitor(),
		rowDisk(),
		rowOS(),
	}}
}

// ---------- 处理器 ----------

// cnNum 1~999 的中文数字（十二/二十/一百二十八），用于"（十二核）"。
func cnNum(n int) string {
	d := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	switch {
	case n < 10:
		return d[n]
	case n < 20:
		if n == 10 {
			return "十"
		}
		return "十" + d[n%10]
	case n < 100:
		s := d[n/10] + "十"
		if r := n % 10; r > 0 {
			s += d[r]
		}
		return s
	case n < 1000:
		s := d[n/100] + "百"
		r := n % 100
		if r == 0 {
			return s
		}
		if r < 10 {
			return s + "零" + d[r]
		}
		return s + cnNum(r)
	}
	return strconv.Itoa(n)
}

var reCoreProcessor = regexp.MustCompile(`(?i)\s+\d+(?:\s*[- ]*core)?\s+processor$`)

func rowCPU() InfoRow {
	type Win32_Processor struct {
		Name          string `wmiconv:"name;Name"`
		NumberOfCores uint32 `wmiconv:"name;NumberOfCores"`
	}
	var procs []Win32_Processor
	if err := wmi.Query("SELECT Name,NumberOfCores FROM Win32_Processor", &procs); err == nil && len(procs) > 0 {
		name := strings.TrimSpace(procs[0].Name)
		cores := int(procs[0].NumberOfCores)
		// WMI 名字常带 "12-Core Processor" 尾巴：去掉后用中文格式补核数。
		if cores > 0 {
			base := reCoreProcessor.ReplaceAllString(name, "")
			if base == "" {
				base = name
			}
			return InfoRow{Label: "处理器", Value: strings.TrimSpace(base) + " " + strconv.Itoa(cores) + "核处理器（" + cnNum(cores) + "核）"}
		}
		if name != "" {
			return InfoRow{Label: "处理器", Value: name}
		}
	}
	return InfoRow{Label: "处理器", Value: "—"}
}

// ---------- 主板 ----------

// vendorMap 常见硬件厂商 → 中文。顺序敏感（长前缀在前）。
var vendorMap = []struct{ key, cn string }{
	{"asustek", "华硕"},
	{"gigabyte", "技嘉"},
	{"micro-star", "微星"},
	{"msi", "微星"},
	{"hewlett", "惠普"},
	{"acer", "宏碁"},
	{"asrock", "华擎"},
	{"inno3d", "影驰"},
	{"colorful", "七彩虹"},
	{"galaxy", "Galaxy"},
	{"samsung", "三星"},
	{"intel", "英特尔"},
	{"lenovo", "联想"},
	{"apple", "苹果"},
	{"dell", "戴尔"},
	{"aoc", "冠捷"},
	{"zotac", "索泰"},
	{"evga", "EVGA"},
	{"pny", "PNY"},
	{"asus", "华硕"},
}

func vendorCN(s string) string {
	if s == "" {
		return ""
	}
	l := strings.ToLower(s)
	for _, e := range vendorMap {
		if strings.Contains(l, e.key) {
			return e.cn
		}
	}
	return ""
}

func rowBoard() InfoRow {
	var bb []struct {
		Manufacturer string `wmiconv:"name;Manufacturer"`
		Product      string `wmiconv:"name;Product"`
	}
	if err := wmi.Query("SELECT Manufacturer,Product FROM Win32_BaseBoard", &bb); err != nil || len(bb) == 0 {
		return InfoRow{Label: "主板", Value: "—"}
	}
	mfr := vendorCN(bb[0].Manufacturer)
	if mfr == "" {
		mfr = strings.TrimSpace(bb[0].Manufacturer)
	}
	product := strings.TrimSpace(bb[0].Product)
	v := strings.TrimSpace(mfr + " " + product)
	if v == "" {
		v = "—"
	}
	return InfoRow{Label: "主板", Value: v}
}

func boardManufacturer() string {
	var bb []struct {
		Manufacturer string `wmiconv:"name;Manufacturer"`
	}
	if err := wmi.Query("SELECT Manufacturer FROM Win32_BaseBoard", &bb); err == nil && len(bb) > 0 {
		return bb[0].Manufacturer
	}
	return ""
}

// ---------- 内存 ----------

// memGen 用内存条标称速度粗略推断代际（DDR 边界值：DDR3≤2133 / DDR4≤3733）。
func memGen(speedMHz int) string {
	switch {
	case speedMHz >= 4800:
		return "DDR5"
	case speedMHz >= 2133:
		return "DDR4"
	case speedMHz >= 800:
		return "DDR3"
	case speedMHz > 0:
		return "DDR2"
	}
	return ""
}

func rowMem() InfoRow {
	vm, err := mem.VirtualMemory()
	if err != nil || vm == nil || vm.Total == 0 {
		return InfoRow{Label: "内存", Value: "—"}
	}
	totalGB := vm.Total / (1 << 30)

	type Win32_PhysicalMemory struct {
		Capacity uint64 `wmiconv:"name;Capacity"`
		Speed    uint64 `wmiconv:"name;Speed"`
	}
	var pms []Win32_PhysicalMemory
	var stickText string
	speed := 0
	n := 0
	if err := wmi.Query("SELECT Capacity,Speed FROM Win32_PhysicalMemory", &pms); err == nil {
		sizes := map[uint64]int{}
		for _, p := range pms {
			if p.Capacity == 0 {
				continue
			}
			s := p.Capacity / (1 << 30)
			sizes[s]++
			n++
			if p.Speed > uint64(speed) {
				speed = int(p.Speed)
			}
		}
		if n > 0 {
			keys := make([]uint64, 0, len(sizes))
			for k := range sizes {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i] > keys[j] })
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				if sizes[k] > 1 {
					parts = append(parts, strconv.FormatUint(k, 10)+"GB × "+strconv.Itoa(sizes[k]))
				} else {
					parts = append(parts, strconv.FormatUint(k, 10)+"GB")
				}
			}
			stickText = strings.Join(parts, " + ")
			if len(keys) == 1 {
				switch n {
				case 1:
					stickText += " 单通道"
				case 2:
					stickText += " 双通道"
				case 4:
					stickText += " 四通道"
				}
			}
		}
	}

	v := strconv.FormatUint(totalGB, 10) + "GB"
	if gen := memGen(speed); gen != "" {
		v += " " + gen + " " + strconv.Itoa(speed) + "MHz"
	}
	if stickText != "" {
		v += "（" + stickText + "）"
	}
	return InfoRow{Label: "内存", Value: v}
}

// ---------- 显卡 ----------

func rowGPU() InfoRow {
	var vcs []struct {
		Name         string `wmiconv:"name;Name"`
		Manufacturer string `wmiconv:"name;Manufacturer"`
		AdapterRAM   uint64 `wmiconv:"name;AdapterRAM"`
	}
	wmiErr := wmi.Query("SELECT Name,Manufacturer,AdapterRAM FROM Win32_VideoController", &vcs)
	nv := nvidiaGPUs()
	if wmiErr != nil || len(vcs) == 0 {
		if len(nv) > 0 {
			return gpuRow(nv)
		}
		return InfoRow{Label: "显卡", Value: "—"}
	}
	boardMfr := boardManufacturer()
	var list []gpuEntry
	used := make([]bool, len(nv))
	for _, vc := range vcs {
		name := strings.TrimSpace(vc.Name)
		if name == "" {
			continue
		}
		// NVIDIA 卡优先用 nvidia-smi 配对（显存最准；WMI 的 AdapterRAM 经常是假值）。
		if isNvidiaName(name) {
			for j := range nv {
				if !used[j] && normGPU(nv[j].name) == normGPU(name) {
					list = append(list, nv[j])
					used[j] = true
					name = ""
					break
				}
			}
		}
		if name == "" {
			continue
		}
		kind := "独立显卡"
		if isIGPUName(name) {
			kind = "集成显卡"
		}
		var vram, vendor string
		if kind == "集成显卡" {
			vram = memBytesStr(vc.AdapterRAM) // 集显共享内存，AdapterRAM 可用
			vendor = vendorCN(boardMfr)       // 集显厂商 = 主板厂商
		} else {
			vendor = vendorCN(vc.Manufacturer)
		}
		list = append(list, gpuEntry{kind: kind, name: name, vram: vram, vendor: vendor})
	}
	return gpuRow(list)
}

func isNvidiaName(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "nvidia") || strings.Contains(l, "geforce") ||
		strings.Contains(l, "quadro") || strings.Contains(l, "rtx")
}

// normGPU 归一化显卡名用于配对：去空白、去 nvidia 前缀、小写。
func normGPU(s string) string {
	l := strings.ToLower(strings.ReplaceAll(s, " ", ""))
	return strings.TrimPrefix(l, "nvidia")
}

func isIGPUName(s string) bool {
	l := strings.ToLower(s)
	if strings.Contains(l, "radeon") && strings.Contains(l, "graphics") {
		return true // AMD 集显 "AMD Radeon(TM) Graphics"
	}
	for _, p := range []string{"uhd graphics", "iris", "intel graphics", "hd graphics"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// nvidiaGPUs 从 nvidia-smi 取卡名 + 显存（MiB）。非 NVIDIA 机器返回 nil。
func nvidiaGPUs() []gpuEntry {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total",
		"--format=csv,noheader,nounits")
	// GUI 进程无控制台，禁止 nvidia-smi 弹出控制台窗口。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	var list []gpuEntry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		vram := ""
		if v, perr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); perr == nil && v > 0 {
			vram = miBToGB(v)
		}
		list = append(list, gpuEntry{kind: "独立显卡", name: name, vram: vram})
	}
	return list
}

func miBToGB(miB int64) string {
	if miB >= 1024 {
		return strconv.FormatUint(uint64((miB+512)/1024), 10) + "GB"
	}
	return strconv.FormatInt(miB, 10) + "MB"
}

func memBytesStr(b uint64) string {
	if b == 0 {
		return ""
	}
	if b >= 1<<30 {
		return strconv.FormatUint(b/(1<<30), 10) + "GB"
	}
	if b >= 1<<20 {
		return strconv.FormatUint(b/(1<<20), 10) + "MB"
	}
	return ""
}

func gpuRow(list []gpuEntry) InfoRow {
	if len(list) == 0 {
		return InfoRow{Label: "显卡", Value: "—"}
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := 0, 0
		if list[i].kind == "集成显卡" {
			ri = 1
		}
		if list[j].kind == "集成显卡" {
			rj = 1
		}
		return ri > rj // 集成在前
	})
	val := func(g gpuEntry) string {
		parts := []string{}
		if g.vram != "" {
			parts = append(parts, g.vram)
		}
		if g.vendor != "" {
			parts = append(parts, g.vendor)
		}
		v := g.name
		if len(parts) > 0 {
			v += "（" + strings.Join(parts, " / ") + "）"
		}
		return v
	}
	if len(list) == 1 {
		return InfoRow{Label: "显卡", Value: val(list[0])}
	}
	subs := make([]InfoRow, 0, len(list))
	for _, g := range list {
		subs = append(subs, InfoRow{Label: g.kind, Value: val(g)})
	}
	return InfoRow{Label: "显卡", Subs: subs}
}

// ---------- 显示器 ----------

var reGenericMonitor = regexp.MustCompile(`(?i)^Generic Monitor \(([^)]+)\)$`)

func monitorName(s string) string {
	s = strings.TrimSpace(s)
	if m := reGenericMonitor.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	switch s {
	case "", "Default Monitor", "Generic PnP Monitor", "Generic Monitor":
		return ""
	}
	return s
}

func rowMonitor() InfoRow {
	var mons []struct {
		Name string `wmiconv:"name;Name"`
	}
	if err := wmi.Query("SELECT Name FROM Win32_PnPEntity WHERE PnpClass='Monitor'", &mons); err != nil {
		return InfoRow{Label: "显示器", Value: "—"}
	}
	// 尺寸（毫米）：能配对到才显示；配不到就只显示型号。
	type Win32_DesktopMonitor struct {
		MaxHorizontalImageSize uint64 `wmiconv:"name;MaxHorizontalImageSize"`
		MaxVerticalImageSize   uint64 `wmiconv:"name;MaxVerticalImageSize"`
	}
	var dms []Win32_DesktopMonitor
	_ = wmi.Query("SELECT MaxHorizontalImageSize,MaxVerticalImageSize FROM Win32_DesktopMonitor", &dms)

	var names []string
	for _, m := range mons {
		if n := monitorName(m.Name); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return InfoRow{Label: "显示器", Value: "—"}
	}
	if len(names) == 1 {
		size := 0.0
		if len(dms) == 1 && dms[0].MaxHorizontalImageSize > 0 && dms[0].MaxVerticalImageSize > 0 {
			size = math.Sqrt(float64(dms[0].MaxHorizontalImageSize)*float64(dms[0].MaxHorizontalImageSize) +
				float64(dms[0].MaxVerticalImageSize)*float64(dms[0].MaxVerticalImageSize)) / 25.4
		}
		v := names[0]
		if size >= 5 {
			v += "（" + strconv.FormatFloat(size, 'f', 1, 64) + "英寸）"
		}
		return InfoRow{Label: "显示器", Value: v}
	}
	subs := make([]InfoRow, 0, len(names))
	for i, n := range names {
		subs = append(subs, InfoRow{Label: "显示器" + strconv.Itoa(i+1), Value: n})
	}
	return InfoRow{Label: "显示器", Subs: subs}
}

// ---------- 磁盘 ----------

var (
	reDriveLetter = regexp.MustCompile(`"([A-Z]):"`)
	reDiskNum     = regexp.MustCompile(`Disk #(\d+)`)
)

// bootDiskIndex 找 C 盘所在的物理盘索引。
// 走 Win32_LogicalDiskToPartition（Antecedent/Dependent 关系字符串解析）——
// 比 Win32_LogicalPartition/Win32_Partition 更可靠（精简镜像常缺这些类）。
func bootDiskIndex() (int, bool) {
	var rows []struct {
		Antecedent string `wmiconv:"name;Antecedent"`
		Dependent  string `wmiconv:"name;Dependent"`
	}
	if err := wmi.Query("SELECT Antecedent,Dependent FROM Win32_LogicalDiskToPartition", &rows); err != nil {
		return -1, false
	}
	for _, r := range rows {
		m := reDriveLetter.FindStringSubmatch(r.Dependent)
		if m != nil && m[1] == "C" {
			if m2 := reDiskNum.FindStringSubmatch(r.Antecedent); m2 != nil {
				if i, err := strconv.Atoi(m2[1]); err == nil {
					return i, true
				}
			}
		}
	}
	return -1, false
}

func rowDisk() InfoRow {
	var dds []struct {
		Index uint32 `wmiconv:"name;Index"`
		Model string `wmiconv:"name;Model"`
		Size  uint64 `wmiconv:"name;Size"`
	}
	if err := wmi.Query("SELECT Index,Model,Size FROM Win32_DiskDrive", &dds); err != nil || len(dds) == 0 {
		return InfoRow{Label: "磁盘", Value: "—"}
	}
	type dinfo struct {
		idx   int
		model string
		size  uint64
	}
	var list []dinfo
	for _, dd := range dds {
		if dd.Size < 1e9 {
			continue // 小于 1GB 的（空 U 盘等）不显示
		}
		model := strings.TrimSpace(dd.Model)
		if model == "" {
			model = "未知型号"
		}
		list = append(list, dinfo{idx: int(dd.Index), model: model, size: dd.Size})
	}
	if len(list) == 0 {
		return InfoRow{Label: "磁盘", Value: "—"}
	}
	boot, hasBoot := bootDiskIndex()
	sort.SliceStable(list, func(i, j int) bool {
		if hasBoot {
			bi, bj := list[i].idx == boot, list[j].idx == boot
			if bi != bj {
				return bi // 主硬盘（C 盘所在盘）排第一
			}
		}
		return list[i].size > list[j].size
	})
	subs := make([]InfoRow, 0, len(list))
	for i, d := range list {
		label := ""
		if i == 0 && hasBoot {
			label = "主硬盘"
		}
		sizeGB := (d.size + 5e8) / 1e9 // 十进制 GB，与市售标称一致（2048GB）
		subs = append(subs, InfoRow{Label: label, Value: d.model + "（" + strconv.FormatUint(sizeGB, 10) + "GB）"})
	}
	if len(subs) == 1 {
		return InfoRow{Label: "磁盘", Value: subs[0].Value} // 单盘不显示「主硬盘」标签
	}
	return InfoRow{Label: "磁盘", Subs: subs}
}

// ---------- 系统 ----------

func rowOS() InfoRow {
	if info, err := host.Info(); err == nil {
		v := strings.TrimSpace(info.OS + " " + info.PlatformVersion)
		if v != "" {
			return InfoRow{Label: "系统", Value: v}
		}
	}
	return InfoRow{Label: "系统", Value: "—"}
}
