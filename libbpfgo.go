package libbpfgo

/*
#cgo LDFLAGS: -lelf -lz

#include <errno.h>
#include <stdlib.h>
#include <sys/resource.h>

#include <bpf/bpf.h>
#include <bpf/libbpf.h>

#ifndef MAX_ERRNO
#define MAX_ERRNO           4095
#define IS_ERR_VALUE(x)     ((x) >= (unsigned long)-MAX_ERRNO)

static inline bool IS_ERR(const void *ptr)
{
    return IS_ERR_VALUE((unsigned long)ptr);
}

static inline bool IS_ERR_OR_NULL(const void *ptr)
{
    return !ptr || IS_ERR_VALUE((unsigned long)ptr);
}

static inline long PTR_ERR(const void *ptr)
{
    return (long) ptr;
}
#endif

int libbpf_print_fn(enum libbpf_print_level level, const char *format,
                    va_list args)
{
    if (level != LIBBPF_WARN)
        return 0;

	// BUG: https://github.com/aquasecurity/tracee/issues/1676

	va_list check; va_copy(check, args);
	char *str = va_arg(check, char *);
	if (strstr(str, "Exclusivity flag on") != NULL) {
		va_end(check);
		return 0;
	}
	va_end(check);

    return vfprintf(stderr, format, args);
}

void set_print_fn() {
    libbpf_set_print(libbpf_print_fn);
}

extern void perfCallback(void *ctx, int cpu, void *data, __u32 size);
extern void perfLostCallback(void *ctx, int cpu, __u64 cnt);
extern int ringbufferCallback(void *ctx, void *data, size_t size);

struct ring_buffer * init_ring_buf(int map_fd, uintptr_t ctx)
{
    struct ring_buffer *rb = NULL;

    rb = ring_buffer__new(map_fd, ringbufferCallback, (void*)ctx, NULL);
    if (!rb) {
        fprintf(stderr, "Failed to initialize ring buffer\n");
        return NULL;
    }

    return rb;
}

struct perf_buffer * init_perf_buf(int map_fd, int page_cnt, uintptr_t ctx)
{
    struct perf_buffer_opts pb_opts = {};
    struct perf_buffer *pb = NULL;

    pb_opts.sz = sizeof(struct perf_buffer_opts);

    pb = perf_buffer__new(map_fd, page_cnt, perfCallback, perfLostCallback,
                          (void *) ctx, &pb_opts);
    if (libbpf_get_error(pb)) {
        fprintf(stderr, "Failed to initialize perf buffer!\n");
        return NULL;
    }

    return pb;
}
*/
import "C"

import (
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/aquasecurity/libbpfgo/helpers"
)

const (
	// Maximum number of channels (RingBuffers + PerfBuffers) supported
	maxEventChannels = 512
)

// MajorVersion returns the major semver version of libbpf.
func MajorVersion() int {
	return C.LIBBPF_MAJOR_VERSION
}

// MinorVersion returns the minor semver version of libbpf.
func MinorVersion() int {
	return C.LIBBPF_MINOR_VERSION
}

// LibbpfVersionString returns the string representation of the libbpf version which
// libbpfgo is linked against
func LibbpfVersionString() string {
	return fmt.Sprintf("v%d.%d", MajorVersion(), MinorVersion())
}

type Module struct {
	obj      *C.struct_bpf_object
	links    []*BPFLink
	perfBufs []*PerfBuffer
	ringBufs []*RingBuffer
}

type BPFMap struct {
	name   string
	bpfMap *C.struct_bpf_map
	fd     C.int
	module *Module
}

type MapType uint32

const (
	MapTypeUnspec MapType = iota
	MapTypeHash
	MapTypeArray
	MapTypeProgArray
	MapTypePerfEventArray
	MapTypePerCPUHash
	MapTypePerCPUArray
	MapTypeStackTrace
	MapTypeCgroupArray
	MapTypeLRUHash
	MapTypeLRUPerCPUHash
	MapTypeLPMTrie
	MapTypeArrayOfMaps
	MapTypeHashOfMaps
	MapTypeDevMap
	MapTypeSockMap
	MapTypeCPUMap
	MapTypeXSKMap
	MapTypeSockHash
	MapTypeCgroupStorage
	MapTypeReusePortSockArray
	MapTypePerCPUCgroupStorage
	MapTypeQueue
	MapTypeStack
	MapTypeSKStorage
	MapTypeDevmapHash
	MapTypeStructOps
	MapTypeRingbuf
	MapTypeInodeStorage
	MapTypeTaskStorage
	MapTypeBloomFilter
)

type MapFlag uint32

const (
	MapFlagUpdateAny     MapFlag = iota // create new element or update existing
	MapFlagUpdateNoExist                // create new element if it didn't exist
	MapFlagUpdateExist                  // update existing element
	MapFlagFLock                        // spin_lock-ed map_lookup/map_update
)

func (m MapType) String() string {
	x := map[MapType]string{
		MapTypeUnspec:              "BPF_MAP_TYPE_UNSPEC",
		MapTypeHash:                "BPF_MAP_TYPE_HASH",
		MapTypeArray:               "BPF_MAP_TYPE_ARRAY",
		MapTypeProgArray:           "BPF_MAP_TYPE_PROG_ARRAY",
		MapTypePerfEventArray:      "BPF_MAP_TYPE_PERF_EVENT_ARRAY",
		MapTypePerCPUHash:          "BPF_MAP_TYPE_PERCPU_HASH",
		MapTypePerCPUArray:         "BPF_MAP_TYPE_PERCPU_ARRAY",
		MapTypeStackTrace:          "BPF_MAP_TYPE_STACK_TRACE",
		MapTypeCgroupArray:         "BPF_MAP_TYPE_CGROUP_ARRAY",
		MapTypeLRUHash:             "BPF_MAP_TYPE_LRU_HASH",
		MapTypeLRUPerCPUHash:       "BPF_MAP_TYPE_LRU_PERCPU_HASH",
		MapTypeLPMTrie:             "BPF_MAP_TYPE_LPM_TRIE",
		MapTypeArrayOfMaps:         "BPF_MAP_TYPE_ARRAY_OF_MAPS",
		MapTypeHashOfMaps:          "BPF_MAP_TYPE_HASH_OF_MAPS",
		MapTypeDevMap:              "BPF_MAP_TYPE_DEVMAP",
		MapTypeSockMap:             "BPF_MAP_TYPE_SOCKMAP",
		MapTypeCPUMap:              "BPF_MAP_TYPE_CPUMAP",
		MapTypeXSKMap:              "BPF_MAP_TYPE_XSKMAP",
		MapTypeSockHash:            "BPF_MAP_TYPE_SOCKHASH",
		MapTypeCgroupStorage:       "BPF_MAP_TYPE_CGROUP_STORAGE",
		MapTypeReusePortSockArray:  "BPF_MAP_TYPE_REUSEPORT_SOCKARRAY",
		MapTypePerCPUCgroupStorage: "BPF_MAP_TYPE_PERCPU_CGROUP_STORAGE",
		MapTypeQueue:               "BPF_MAP_TYPE_QUEUE",
		MapTypeStack:               "BPF_MAP_TYPE_STACK",
		MapTypeSKStorage:           "BPF_MAP_TYPE_SK_STORAGE",
		MapTypeDevmapHash:          "BPF_MAP_TYPE_DEVMAP_HASH",
		MapTypeStructOps:           "BPF_MAP_TYPE_STRUCT_OPS",
		MapTypeRingbuf:             "BPF_MAP_TYPE_RINGBUF",
		MapTypeInodeStorage:        "BPF_MAP_TYPE_INODE_STORAGE",
		MapTypeTaskStorage:         "BPF_MAP_TYPE_TASK_STORAGE",
		MapTypeBloomFilter:         "BPF_MAP_TYPE_BLOOM_FILTER",
	}
	return x[m]
}

type BPFProg struct {
	name       string
	prog       *C.struct_bpf_program
	module     *Module
	pinnedPath string
}

type LinkType int

const (
	Tracepoint LinkType = iota
	RawTracepoint
	Kprobe
	Kretprobe
	LSM
	PerfEvent
	Uprobe
	Uretprobe
	Tracing
	XDP
)

type BPFLink struct {
	link      *C.struct_bpf_link
	prog      *BPFProg
	linkType  LinkType
	eventName string
}

func (l *BPFLink) Destroy() error {
	ret := C.bpf_link__destroy(l.link)
	if ret < 0 {
		return syscall.Errno(-ret)
	}
	l.link = nil

	return nil
}

func (l *BPFLink) GetFd() int {
	return int(C.bpf_link__fd(l.link))
}

func (l *BPFLink) Pin(pinPath string) error {
	path := C.CString(pinPath)
	errC := C.bpf_link__pin(l.link, path)
	C.free(unsafe.Pointer(path))
	if errC != 0 {
		return fmt.Errorf("failed to pin link %s to path %s: %w", l.eventName, pinPath, syscall.Errno(-errC))
	}
	return nil
}

func (l *BPFLink) Unpin(pinPath string) error {
	path := C.CString(pinPath)
	errC := C.bpf_link__unpin(l.link)
	C.free(unsafe.Pointer(path))
	if errC != 0 {
		return fmt.Errorf("failed to unpin link %s from path %s: %w", l.eventName, pinPath, syscall.Errno(-errC))
	}
	return nil
}

type PerfBuffer struct {
	pb         *C.struct_perf_buffer
	bpfMap     *BPFMap
	slot       uint
	eventsChan chan []byte
	lostChan   chan uint64
	stop       chan struct{}
	closed     bool
	wg         sync.WaitGroup
}

type RingBuffer struct {
	rb     *C.struct_ring_buffer
	bpfMap *BPFMap
	slot   uint
	stop   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// BPF is using locked memory for BPF maps and various other things.
// By default, this limit is very low - increase to avoid failures
func bumpMemlockRlimit() error {
	var rLimit syscall.Rlimit
	rLimit.Max = 512 << 20 /* 512 MBs */
	rLimit.Cur = 512 << 20 /* 512 MBs */
	err := syscall.Setrlimit(C.RLIMIT_MEMLOCK, &rLimit)
	if err != nil {
		return fmt.Errorf("error setting rlimit: %v", err)
	}
	return nil
}

func errptrError(ptr unsafe.Pointer, format string, args ...interface{}) error {
	negErrno := C.PTR_ERR(ptr)
	errno := syscall.Errno(-int64(negErrno))
	if errno == 0 {
		return fmt.Errorf(format, args...)
	}

	args = append(args, errno.Error())
	return fmt.Errorf(format+": %v", args...)
}

type NewModuleArgs struct {
	KConfigFilePath string
	BTFObjPath      string
	BPFObjName      string
	BPFObjPath      string
	BPFObjBuff      []byte
}

func NewModuleFromFile(bpfObjPath string) (*Module, error) {

	return NewModuleFromFileArgs(NewModuleArgs{
		BPFObjPath: bpfObjPath,
	})
}

// LibbpfStrictMode is an enum as defined in https://github.com/libbpf/libbpf/blob/2cd2d03f63242c048a896179398c68d2dbefe3d6/src/libbpf_legacy.h#L23
type LibbpfStrictMode uint32

const (
	LibbpfStrictModeAll               LibbpfStrictMode = C.LIBBPF_STRICT_ALL
	LibbpfStrictModeNone              LibbpfStrictMode = C.LIBBPF_STRICT_NONE
	LibbpfStrictModeCleanPtrs         LibbpfStrictMode = C.LIBBPF_STRICT_CLEAN_PTRS
	LibbpfStrictModeDirectErrs        LibbpfStrictMode = C.LIBBPF_STRICT_DIRECT_ERRS
	LibbpfStrictModeSecName           LibbpfStrictMode = C.LIBBPF_STRICT_SEC_NAME
	LibbpfStrictModeNoObjectList      LibbpfStrictMode = C.LIBBPF_STRICT_NO_OBJECT_LIST
	LibbpfStrictModeAutoRlimitMemlock LibbpfStrictMode = C.LIBBPF_STRICT_AUTO_RLIMIT_MEMLOCK
	LibbpfStrictModeMapDefinitions    LibbpfStrictMode = C.LIBBPF_STRICT_MAP_DEFINITIONS
)

func (b LibbpfStrictMode) String() (str string) {
	x := map[LibbpfStrictMode]string{
		LibbpfStrictModeAll:               "LIBBPF_STRICT_ALL",
		LibbpfStrictModeNone:              "LIBBPF_STRICT_NONE",
		LibbpfStrictModeCleanPtrs:         "LIBBPF_STRICT_CLEAN_PTRS",
		LibbpfStrictModeDirectErrs:        "LIBBPF_STRICT_DIRECT_ERRS",
		LibbpfStrictModeSecName:           "LIBBPF_STRICT_SEC_NAME",
		LibbpfStrictModeNoObjectList:      "LIBBPF_STRICT_NO_OBJECT_LIST",
		LibbpfStrictModeAutoRlimitMemlock: "LIBBPF_STRICT_AUTO_RLIMIT_MEMLOCK",
		LibbpfStrictModeMapDefinitions:    "LIBBPF_STRICT_MAP_DEFINITIONS",
	}

	str, ok := x[b]
	if !ok {
		str = LibbpfStrictModeNone.String()
	}
	return str
}

func SetStrictMode(mode LibbpfStrictMode) {
	C.libbpf_set_strict_mode(uint32(mode))
}

func NewModuleFromFileArgs(args NewModuleArgs) (*Module, error) {
	C.set_print_fn()
	if err := bumpMemlockRlimit(); err != nil {
		return nil, err
	}
	opts := C.struct_bpf_object_open_opts{}
	opts.sz = C.sizeof_struct_bpf_object_open_opts

	bpfFile := C.CString(args.BPFObjPath)
	defer C.free(unsafe.Pointer(bpfFile))

	// instruct libbpf to use user provided kernel BTF file
	if args.BTFObjPath != "" {
		btfFile := C.CString(args.BTFObjPath)
		opts.btf_custom_path = btfFile
		defer C.free(unsafe.Pointer(btfFile))
	}

	// instruct libbpf to use user provided KConfigFile
	if args.KConfigFilePath != "" {
		kConfigFile := C.CString(args.KConfigFilePath)
		opts.kconfig = kConfigFile
		defer C.free(unsafe.Pointer(kConfigFile))
	}

	obj := C.bpf_object__open_file(bpfFile, &opts)
	if C.IS_ERR_OR_NULL(unsafe.Pointer(obj)) {
		return nil, errptrError(unsafe.Pointer(obj), "failed to open BPF object %s", args.BPFObjPath)
	}

	return &Module{
		obj: obj,
	}, nil
}

func NewModuleFromBuffer(bpfObjBuff []byte, bpfObjName string) (*Module, error) {

	return NewModuleFromBufferArgs(NewModuleArgs{
		BPFObjBuff: bpfObjBuff,
		BPFObjName: bpfObjName,
	})
}

func NewModuleFromBufferArgs(args NewModuleArgs) (*Module, error) {
	C.set_print_fn()
	if err := bumpMemlockRlimit(); err != nil {
		return nil, err
	}
	if args.BTFObjPath == "" {
		args.BTFObjPath = "/sys/kernel/btf/vmlinux"
	}
	btfFile := C.CString(args.BTFObjPath)
	bpfName := C.CString(args.BPFObjName)
	bpfBuff := unsafe.Pointer(C.CBytes(args.BPFObjBuff))
	bpfBuffSize := C.size_t(len(args.BPFObjBuff))

	opts := C.struct_bpf_object_open_opts{}
	opts.object_name = bpfName
	opts.sz = C.sizeof_struct_bpf_object_open_opts
	opts.btf_custom_path = btfFile // instruct libbpf to use user provided kernel BTF file

	if len(args.KConfigFilePath) > 2 {
		kConfigFile := C.CString(args.KConfigFilePath)
		opts.kconfig = kConfigFile // instruct libbpf to use user provided KConfigFile
		defer C.free(unsafe.Pointer(kConfigFile))
	}

	obj := C.bpf_object__open_mem(bpfBuff, bpfBuffSize, &opts)
	if C.IS_ERR_OR_NULL(unsafe.Pointer(obj)) {
		return nil, errptrError(unsafe.Pointer(obj), "failed to open BPF object %s: %v", args.BPFObjName, args.BPFObjBuff[:20])
	}

	C.free(bpfBuff)
	C.free(unsafe.Pointer(bpfName))
	C.free(unsafe.Pointer(btfFile))

	return &Module{
		obj: obj,
	}, nil
}

func (m *Module) Close() {
	for _, pb := range m.perfBufs {
		pb.Close()
	}
	for _, rb := range m.ringBufs {
		rb.Close()
	}
	for _, link := range m.links {
		if link.link != nil {
			C.bpf_link__destroy(link.link)
		}
	}
	C.bpf_object__close(m.obj)
}

func (m *Module) BPFLoadObject() error {
	ret := C.bpf_object__load(m.obj)
	if ret != 0 {
		return fmt.Errorf("failed to load BPF object")
	}

	return nil
}

// BPFMapCreateOpts mirrors the C structure bpf_map_create_opts
type BPFMapCreateOpts struct {
	Size                  uint64
	BtfFD                 uint32
	BtfKeyTypeID          uint32
	BtfValueTypeID        uint32
	BtfVmlinuxValueTypeID uint32
	InnerMapFD            uint32
	MapFlags              uint32
	MapExtra              uint64
	NumaNode              uint32
	MapIfIndex            uint32
}

func bpfMapCreateOptsToC(createOpts *BPFMapCreateOpts) *C.struct_bpf_map_create_opts {
	if createOpts == nil {
		return nil
	}
	opts := C.struct_bpf_map_create_opts{}
	opts.sz = C.ulong(createOpts.Size)
	opts.btf_fd = C.uint(createOpts.BtfFD)
	opts.btf_key_type_id = C.uint(createOpts.BtfKeyTypeID)
	opts.btf_value_type_id = C.uint(createOpts.BtfValueTypeID)
	opts.btf_vmlinux_value_type_id = C.uint(createOpts.BtfVmlinuxValueTypeID)
	opts.inner_map_fd = C.uint(createOpts.InnerMapFD)
	opts.map_flags = C.uint(createOpts.MapFlags)
	opts.map_extra = C.ulonglong(createOpts.MapExtra)
	opts.numa_node = C.uint(createOpts.NumaNode)
	opts.map_ifindex = C.uint(createOpts.MapIfIndex)

	return &opts
}

// CreateMap creates a BPF map from userspace. This can be used for populating
// BPF array of maps or hash of maps. However, this function uses a low-level
// libbpf API; maps created in this way do not conform to libbpf map formats,
// and therefore do not have access to libbpf high level bpf_map__* APIS
// which causes different behavior from maps created in the kernel side code
//
// See usage of `bpf_map_create()` in kernel selftests for more info
func CreateMap(mapType MapType, mapName string, keySize, valueSize, maxEntries int, opts *BPFMapCreateOpts) (*BPFMap, error) {
	cs := C.CString(mapName)
	fdOrError := C.bpf_map_create(uint32(mapType), cs, C.uint(keySize), C.uint(valueSize), C.uint(maxEntries), bpfMapCreateOptsToC(opts))
	C.free(unsafe.Pointer(cs))
	if fdOrError < 0 {
		return nil, fmt.Errorf("could not create map: %w", syscall.Errno(-fdOrError))
	}

	return &BPFMap{
		name:   mapName,
		fd:     fdOrError,
		module: nil,
		bpfMap: nil,
	}, nil
}

func (m *Module) GetMap(mapName string) (*BPFMap, error) {
	cs := C.CString(mapName)
	bpfMap, errno := C.bpf_object__find_map_by_name(m.obj, cs)
	C.free(unsafe.Pointer(cs))
	if bpfMap == nil {
		return nil, fmt.Errorf("failed to find BPF map %s: %w", mapName, errno)
	}

	return &BPFMap{
		bpfMap: bpfMap,
		name:   mapName,
		fd:     C.bpf_map__fd(bpfMap),
		module: m,
	}, nil
}

func (b *BPFMap) Name() string {
	cs := C.bpf_map__name(b.bpfMap)
	if cs == nil {
		return ""
	}
	s := C.GoString(cs)
	return s
}

func (b *BPFMap) Type() MapType {
	return MapType(C.bpf_map__type(b.bpfMap))
}

// SetType is used to set the type of a bpf map that isn't associated
// with a file descriptor already. If the map is already associated
// with a file descriptor the libbpf API will return error code EBUSY
func (b *BPFMap) SetType(mapType MapType) error {
	errC := C.bpf_map__set_type(b.bpfMap, C.enum_bpf_map_type(int(mapType)))
	if errC != 0 {
		return fmt.Errorf("could not set bpf map type: %w", syscall.Errno(-errC))
	}
	return nil
}

func (b *BPFMap) Pin(pinPath string) error {
	path := C.CString(pinPath)
	ret, errC := C.bpf_map__pin(b.bpfMap, path)
	C.free(unsafe.Pointer(path))
	if ret != 0 {
		return fmt.Errorf("failed to pin map %s to path %s: %w", b.name, pinPath, errC)
	}
	return nil
}

func (b *BPFMap) Unpin(pinPath string) error {
	path := C.CString(pinPath)
	ret, errC := C.bpf_map__unpin(b.bpfMap, path)
	C.free(unsafe.Pointer(path))
	if ret != 0 {
		return fmt.Errorf("failed to unpin map %s from path %s: %w", b.name, pinPath, errC)
	}
	return nil
}

func (b *BPFMap) SetPinPath(pinPath string) error {
	path := C.CString(pinPath)
	ret, errC := C.bpf_map__set_pin_path(b.bpfMap, path)
	C.free(unsafe.Pointer(path))
	if ret != 0 {
		return fmt.Errorf("failed to set pin for map %s to path %s: %w", b.name, pinPath, errC)
	}
	return nil
}

// Resize changes the map's capacity to maxEntries.
// It should be called after the module was initialized but
// prior to it being loaded with BPFLoadObject.
// Note: for ring buffer and perf buffer, maxEntries is the
// capacity in bytes.
func (b *BPFMap) Resize(maxEntries uint32) error {
	ret, errC := C.bpf_map__set_max_entries(b.bpfMap, C.uint(maxEntries))
	if ret != 0 {
		return fmt.Errorf("failed to resize map %s to %v: %w", b.name, maxEntries, errC)
	}
	return nil
}

// GetMaxEntries returns the map's capacity.
// Note: for ring buffer and perf buffer, maxEntries is the
// capacity in bytes.
func (b *BPFMap) GetMaxEntries() uint32 {
	maxEntries := C.bpf_map__max_entries(b.bpfMap)
	return uint32(maxEntries)
}

func (b *BPFMap) GetFd() int {
	return int(b.fd)
}

func (b *BPFMap) GetName() string {
	return b.name
}

func (b *BPFMap) GetModule() *Module {
	return b.module
}

func (b *BPFMap) GetPinPath() string {
	pinPathGo := C.GoString(C.bpf_map__get_pin_path(b.bpfMap))
	return pinPathGo
}

func (b *BPFMap) IsPinned() bool {
	isPinned := C.bpf_map__is_pinned(b.bpfMap)
	return isPinned == C.bool(true)
}

func (b *BPFMap) KeySize() int {
	return int(C.bpf_map__key_size(b.bpfMap))
}

func (b *BPFMap) ValueSize() int {
	return int(C.bpf_map__value_size(b.bpfMap))
}

func (b *BPFMap) SetValueSize(size uint32) error {
	errC := C.bpf_map__set_value_size(b.bpfMap, C.uint(size))
	if errC != 0 {
		return fmt.Errorf("could not set map value size: %w", syscall.Errno(-errC))
	}
	return nil
}

// GetValue takes a pointer to the key which is stored in the map.
// It returns the associated value as a slice of bytes.
// All basic types, and structs are supported as keys.
//
// NOTE: Slices and arrays are also supported but special care
// should be taken as to take a reference to the first element
// in the slice or array instead of the slice/array itself, as to
// avoid undefined behavior.
func (b *BPFMap) GetValue(key unsafe.Pointer) ([]byte, error) {
	value := make([]byte, b.ValueSize())
	valuePtr := unsafe.Pointer(&value[0])

	ret, errC := C.bpf_map_lookup_elem(b.fd, key, valuePtr)
	if ret != 0 {
		return nil, fmt.Errorf("failed to lookup value %v in map %s: %w", key, b.name, errC)
	}
	return value, nil
}

func (b *BPFMap) GetValueFlags(key unsafe.Pointer, flags MapFlag) ([]byte, error) {
	value := make([]byte, b.ValueSize())
	valuePtr := unsafe.Pointer(&value[0])

	errC := C.bpf_map_lookup_elem_flags(b.fd, key, valuePtr, C.ulonglong(flags))
	if errC != 0 {
		return nil, fmt.Errorf("failed to lookup value %v in map %s: %w", key, b.name, syscall.Errno(-errC))
	}
	return value, nil
}

// GetValueReadInto is like GetValue, except it allows the caller to pass in
// a pointer to the slice of bytes that the value would be read into from the
// map.
// This is useful for reading from maps with variable sizes, especially
// per-cpu arrays and hash maps where the size of each value depends on the
// number of CPUs
func (b *BPFMap) GetValueReadInto(key unsafe.Pointer, value *[]byte) error {
	valuePtr := unsafe.Pointer(&(*value)[0])
	errC := C.bpf_map__lookup_elem(b.bpfMap, key, C.ulong(b.KeySize()), valuePtr, C.ulong(len(*value)), 0)
	if errC != 0 {
		return fmt.Errorf("failed to lookup value %v in map %s: %w", key, b.name, syscall.Errno(-errC))
	}
	return nil
}

// BPFMapBatchOpts mirrors the C structure bpf_map_batch_opts.
type BPFMapBatchOpts struct {
	Sz        uint64
	ElemFlags uint64
	Flags     uint64
}

func bpfMapBatchOptsToC(batchOpts *BPFMapBatchOpts) *C.struct_bpf_map_batch_opts {
	if batchOpts == nil {
		return nil
	}
	opts := C.struct_bpf_map_batch_opts{}
	opts.sz = C.ulong(batchOpts.Sz)
	opts.elem_flags = C.ulonglong(batchOpts.ElemFlags)
	opts.flags = C.ulonglong(batchOpts.Flags)

	return &opts
}

// GetValueBatch allows for batch lookups of multiple keys from the map.
//
// The first argument, keys, is a pointer to an array or slice of keys which will be populated with the keys returned from this operation.
// It returns the associated values as a slice of slices of bytes.
//
// This API allows for batch lookups of multiple keys, potentially in steps over multiple iterations. For example,
// you provide the last key seen (or nil) for the startKey, and the first key to start the next iteration with in nextKey.
// Once the first iteration is complete you can provide the last key seen in the previous iteration as the startKey for the next iteration
// and repeat until nextKey is nil.
//
// The last argument, count, is the number of keys to lookup. Passing an argument that greater than the number of keys
// in the map will cause the function to return a syscall.EPERM as an error.
//
// The API can return partial results even though an error is returned.
// In this case the keys that were successfully retrieved until an error occurred will be in the result slice.
func (b *BPFMap) GetValueBatch(keys unsafe.Pointer, startKey, nextKey unsafe.Pointer, count uint32) ([][]byte, error) {
	var (
		values    = make([]byte, b.ValueSize()*int(count))
		valuesPtr = unsafe.Pointer(&values[0])
		countC    = C.uint(count)
	)

	opts := &BPFMapBatchOpts{
		Sz:        uint64(unsafe.Sizeof(BPFMapBatchOpts{})),
		ElemFlags: C.BPF_ANY,
		Flags:     C.BPF_ANY,
	}

	errC := C.bpf_map_lookup_batch(b.fd, startKey, nextKey, keys, valuesPtr, &countC, bpfMapBatchOptsToC(opts))
	if errC != 0 {
		sc := syscall.Errno(-errC)
		if sc != syscall.EFAULT {
			if uint32(countC) != count {
				return collectBatchValues(values, uint32(countC), b.ValueSize()),
					fmt.Errorf("failed to retrieve ALL elements in map %s, fetched (%d/%d): %w", b.name, uint32(countC), count, sc)
			}
		}
		return nil, fmt.Errorf("failed to batch lookup values %v in map %s: %w", keys, b.name, syscall.Errno(-errC))
	}

	return collectBatchValues(values, count, b.ValueSize()), nil
}

// GetValueAndDeleteBatch allows for batch lookup and deletion of elements where each element is deleted after being retrieved from the map.
//
// The first argument, keys, is a pointer to an array or slice of keys which will be populated with the keys returned from this operation.
// It returns the associated values as a slice of slices of bytes.
//
// This API allows for batch lookups and deletion of multiple keys, potentially in steps over multiple iterations. For example,
// you provide the last key seen (or nil) for the startKey, and the first key to start the next iteration with in nextKey.
// Once the first iteration is complete you can provide the last key seen in the previous iteration as the startKey for the next iteration
// and repeat until nextKey is nil.
//
// The last argument, count, is the number of keys to lookup and delete. The kernel will update it with the count of the elements that were
// retrieved and deleted.
//
// The API can return partial results even though an -1 is returned. In this case, errno will be set to `ENOENT` and the values slice and count
// will be filled in with the elements that were read. See the comment below for more context.
func (b *BPFMap) GetValueAndDeleteBatch(keys, startKey, nextKey unsafe.Pointer, count uint32) ([][]byte, error) {
	var (
		values    = make([]byte, b.ValueSize()*int(count))
		valuesPtr = unsafe.Pointer(&values[0])
		countC    = C.uint(count)
	)

	opts := &BPFMapBatchOpts{
		Sz:        uint64(unsafe.Sizeof(BPFMapBatchOpts{})),
		ElemFlags: C.BPF_ANY,
		Flags:     C.BPF_ANY,
	}

	// Before libbpf 1.0 (without LIBBPF_STRICT_DIRECT_ERRS), the return value
	// and errno are not modified [1]. On error, we will get a return value of
	// -1 and errno will be set accordingly with most BPF calls.
	//
	// The batch APIs are a bit different in which they can return an error, but
	// depending on the errno code, it might mean a complete error (nothing was
	// done) or a partial success (some elements were processed).
	//
	// - On complete sucess, it will return 0, and errno won't be set.
	// - On partial sucess, it will return -1, and errno will be set to ENOENT.
	// - On error, it will return -1, and an errno different to ENOENT.
	//
	// [1] https://github.com/libbpf/libbpf/blob/b69f8ee93ef6aa3518f8fbfd9d1df6c2c84fd08f/src/libbpf_internal.h#L496
	ret, errC := C.bpf_map_lookup_and_delete_batch(
		b.fd,
		startKey,
		nextKey,
		keys,
		valuesPtr,
		&countC,
		bpfMapBatchOptsToC(opts))

	processed := uint32(countC)

	if ret != 0 && errC != syscall.ENOENT {
		// ret = -1 && errno == syscall.ENOENT indicates a partial read and delete.
		return nil, fmt.Errorf("failed to batch lookup and delete values %v in map %s: ret %d (err: %s)", keys, b.name, ret, errC)
	}

	// Either some or all entries were read and deleted.
	parsedVals := collectBatchValues(values, processed, b.ValueSize())
	return parsedVals, nil
}

func collectBatchValues(values []byte, count uint32, valueSize int) [][]byte {
	var value []byte
	var collected [][]byte
	for i := 0; i < int(count*uint32(valueSize)); i += valueSize {
		value = values[i : i+valueSize]
		collected = append(collected, value)
	}
	return collected
}

// UpdateBatch updates multiple elements in the map by specified keys and their corresponding values.
//
// The first argument, keys, is a pointer to an array or slice of keys which will be updated using the second argument, values.
// It returns the associated error if any occurred.
//
// The last argument, count, is the number of keys to update. Passing an argument that greater than the number of keys
// in the map will cause the function to return a syscall.EPERM as an error.
func (b *BPFMap) UpdateBatch(keys, values unsafe.Pointer, count uint32) error {
	countC := C.uint(count)

	opts := BPFMapBatchOpts{
		Sz:        uint64(unsafe.Sizeof(BPFMapBatchOpts{})),
		ElemFlags: C.BPF_ANY,
		Flags:     C.BPF_ANY,
	}

	errC := C.bpf_map_update_batch(b.fd, keys, values, &countC, bpfMapBatchOptsToC(&opts))
	if errC != 0 {
		sc := syscall.Errno(-errC)
		if sc != syscall.EFAULT {
			if uint32(countC) != count {
				return fmt.Errorf("failed to update ALL elements in map %s, updated (%d/%d): %w", b.name, uint32(countC), count, sc)
			}
		}
		return fmt.Errorf("failed to batch update elements in map %s: %w", b.name, syscall.Errno(-errC))
	}

	return nil
}

// DeleteKeyBatch allows for batch deletion of multiple elements in the map.
//
// `count` number of keys will be deleted from the map. Passing an argument that greater than the number of keys
// in the map will cause the function to return a syscall.EPERM as an error.
func (b *BPFMap) DeleteKeyBatch(keys unsafe.Pointer, count uint32) error {
	countC := C.uint(count)

	opts := &BPFMapBatchOpts{
		Sz:        uint64(unsafe.Sizeof(BPFMapBatchOpts{})),
		ElemFlags: C.BPF_ANY,
		Flags:     C.BPF_ANY,
	}

	errC := C.bpf_map_delete_batch(b.fd, keys, &countC, bpfMapBatchOptsToC(opts))
	if errC != 0 {
		sc := syscall.Errno(-errC)
		if sc != syscall.EFAULT {
			if uint32(countC) != count {
				return fmt.Errorf("failed to batch delete ALL keys from map %s, deleted (%d/%d): %w", b.name, uint32(countC), count, sc)
			}
		}
		return fmt.Errorf("failed to batch delete keys from map %s: %w", b.name, syscall.Errno(-errC))
	}

	return nil
}

// DeleteKey takes a pointer to the key which is stored in the map.
// It removes the key and associated value from the BPFMap.
// All basic types, and structs are supported as keys.
//
// NOTE: Slices and arrays are also supported but special care
// should be taken as to take a reference to the first element
// in the slice or array instead of the slice/array itself, as to
// avoid undefined behavior.
func (b *BPFMap) DeleteKey(key unsafe.Pointer) error {
	ret, errC := C.bpf_map_delete_elem(b.fd, key)
	if ret != 0 {
		return fmt.Errorf("failed to get lookup key %d from map %s: %w", key, b.name, errC)
	}
	return nil
}

// Update takes a pointer to a key and a value to associate it with in
// the BPFMap. The unsafe.Pointer should be taken on a reference to the
// underlying datatype. All basic types, and structs are supported
//
// NOTE: Slices and arrays are supported but references should be passed
// to the first element in the slice or array.
//
// For example:
//
//  key := 1
//  value := []byte{'a', 'b', 'c'}
//  keyPtr := unsafe.Pointer(&key)
//  valuePtr := unsafe.Pointer(&value[0])
//  bpfmap.Update(keyPtr, valuePtr)
//
func (b *BPFMap) Update(key, value unsafe.Pointer) error {
	return b.UpdateValueFlags(key, value, MapFlagUpdateAny)
}

func (b *BPFMap) UpdateValueFlags(key, value unsafe.Pointer, flags MapFlag) error {
	errC := C.bpf_map_update_elem(b.fd, key, value, C.ulonglong(flags))
	if errC != 0 {
		return fmt.Errorf("failed to update map %s: %w", b.name, syscall.Errno(-errC))
	}
	return nil
}

// BPFObjectProgramIterator iterates over maps in a BPF object
type BPFObjectIterator struct {
	m        *Module
	prevProg *BPFProg
	prevMap  *BPFMap
}

func (m *Module) Iterator() *BPFObjectIterator {
	return &BPFObjectIterator{
		m:        m,
		prevProg: nil,
		prevMap:  nil,
	}
}

func (it *BPFObjectIterator) NextMap() *BPFMap {
	var startMap *C.struct_bpf_map
	if it.prevMap != nil && it.prevMap.bpfMap != nil {
		startMap = it.prevMap.bpfMap
	}

	m := C.bpf_object__next_map(it.m.obj, startMap)
	if m == nil {
		return nil
	}
	cName := C.bpf_map__name(m)

	bpfMap := &BPFMap{
		name:   C.GoString(cName),
		bpfMap: m,
		module: it.m,
	}

	it.prevMap = bpfMap
	return bpfMap
}

func (it *BPFObjectIterator) NextProgram() *BPFProg {

	var startProg *C.struct_bpf_program
	if it.prevProg != nil && it.prevProg.prog != nil {
		startProg = it.prevProg.prog
	}

	p := C.bpf_object__next_program(it.m.obj, startProg)
	if p == nil {
		return nil
	}
	cName := C.bpf_program__name(p)

	prog := &BPFProg{
		name:   C.GoString(cName),
		prog:   p,
		module: it.m,
	}
	it.prevProg = prog
	return prog
}

// BPFMapIterator iterates over keys in a BPF map
type BPFMapIterator struct {
	b    *BPFMap
	err  error
	prev []byte
	next []byte
}

func (b *BPFMap) Iterator() *BPFMapIterator {
	return &BPFMapIterator{
		b:    b,
		prev: nil,
		next: nil,
	}
}

func (it *BPFMapIterator) Next() bool {
	if it.err != nil {
		return false
	}

	prevPtr := unsafe.Pointer(nil)
	if it.next != nil {
		prevPtr = unsafe.Pointer(&it.next[0])
	}

	next := make([]byte, it.b.KeySize())
	nextPtr := unsafe.Pointer(&next[0])

	errC, err := C.bpf_map_get_next_key(it.b.fd, prevPtr, nextPtr)
	if errno, ok := err.(syscall.Errno); errC == -1 && ok && errno == C.ENOENT {
		return false
	}
	if err != nil {
		it.err = err
		return false
	}

	it.prev = it.next
	it.next = next

	return true
}

// Key returns the current key value of the iterator, if the most recent call to Next returned true.
// The slice is valid only until the next call to Next.
func (it *BPFMapIterator) Key() []byte {
	return it.next
}

// Err returns the last error that ocurred while table.Iter or iter.Next
func (it *BPFMapIterator) Err() error {
	return it.err
}

func (m *Module) GetProgram(progName string) (*BPFProg, error) {
	cs := C.CString(progName)
	prog, errC := C.bpf_object__find_program_by_name(m.obj, cs)
	C.free(unsafe.Pointer(cs))
	if prog == nil {
		return nil, fmt.Errorf("failed to find BPF program %s: %w", progName, errC)
	}

	return &BPFProg{
		name:   progName,
		prog:   prog,
		module: m,
	}, nil
}

func (p *BPFProg) GetFd() int {
	return int(C.bpf_program__fd(p.prog))
}

func (p *BPFProg) Pin(path string) error {

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %s: %v", path, err)
	}

	cs := C.CString(absPath)
	ret, errC := C.bpf_program__pin(p.prog, cs)
	C.free(unsafe.Pointer(cs))
	if ret != 0 {
		return fmt.Errorf("failed to pin program %s to %s: %w", p.name, path, errC)
	}
	p.pinnedPath = absPath
	return nil
}

func (p *BPFProg) Unpin(path string) error {
	cs := C.CString(path)
	ret, errC := C.bpf_program__unpin(p.prog, cs)
	C.free(unsafe.Pointer(cs))
	if ret != 0 {
		return fmt.Errorf("failed to unpin program %s to %s: %w", p.name, path, errC)
	}
	p.pinnedPath = ""
	return nil
}

func (p *BPFProg) GetModule() *Module {
	return p.module
}

func (p *BPFProg) GetName() string {
	return p.name
}

func (p *BPFProg) GetSectionName() string {
	cs := C.bpf_program__section_name(p.prog)
	gs := C.GoString(cs)
	return gs
}

func (p *BPFProg) GetPinPath() string {
	return p.pinnedPath
}

// BPFProgType is an enum as defined in https://elixir.bootlin.com/linux/latest/source/include/uapi/linux/bpf.h
type BPFProgType uint32

const (
	BPFProgTypeUnspec BPFProgType = iota
	BPFProgTypeSocketFilter
	BPFProgTypeKprobe
	BPFProgTypeSchedCls
	BPFProgTypeSchedAct
	BPFProgTypeTracepoint
	BPFProgTypeXdp
	BPFProgTypePerfEvent
	BPFProgTypeCgroupSkb
	BPFProgTypeCgroupSock
	BPFProgTypeLwtIn
	BPFProgTypeLwtOut
	BPFProgTypeLwtXmit
	BPFProgTypeSockOps
	BPFProgTypeSkSkb
	BPFProgTypeCgroupDevice
	BPFProgTypeSkMsg
	BPFProgTypeRawTracepoint
	BPFProgTypeCgroupSockAddr
	BPFProgTypeLwtSeg6Local
	BPFProgTypeLircMode2
	BPFProgTypeSkReuseport
	BPFProgTypeFlowDissector
	BPFProgTypeCgroupSysctl
	BPFProgTypeRawTracepointWritable
	BPFProgTypeCgroupSockopt
	BPFProgTypeTracing
	BPFProgTypeStructOps
	BPFProgTypeExt
	BPFProgTypeLsm
	BPFProgTypeSkLookup
	BPFProgTypeSyscall
)

func (b BPFProgType) String() (str string) {
	x := map[BPFProgType]string{
		BPFProgTypeUnspec:                "BPF_PROG_TYPE_UNSPEC",
		BPFProgTypeSocketFilter:          "BPF_PROG_TYPE_SOCKET_FILTER",
		BPFProgTypeKprobe:                "BPF_PROG_TYPE_KPROBE",
		BPFProgTypeSchedCls:              "BPF_PROG_TYPE_SCHED_CLS",
		BPFProgTypeSchedAct:              "BPF_PROG_TYPE_SCHED_ACT",
		BPFProgTypeTracepoint:            "BPF_PROG_TYPE_TRACEPOINT",
		BPFProgTypeXdp:                   "BPF_PROG_TYPE_XDP",
		BPFProgTypePerfEvent:             "BPF_PROG_TYPE_PERF_EVENT",
		BPFProgTypeCgroupSkb:             "BPF_PROG_TYPE_CGROUP_SKB",
		BPFProgTypeCgroupSock:            "BPF_PROG_TYPE_CGROUP_SOCK",
		BPFProgTypeLwtIn:                 "BPF_PROG_TYPE_LWT_IN",
		BPFProgTypeLwtOut:                "BPF_PROG_TYPE_LWT_OUT",
		BPFProgTypeLwtXmit:               "BPF_PROG_TYPE_LWT_XMIT",
		BPFProgTypeSockOps:               "BPF_PROG_TYPE_SOCK_OPS",
		BPFProgTypeSkSkb:                 "BPF_PROG_TYPE_SK_SKB",
		BPFProgTypeCgroupDevice:          "BPF_PROG_TYPE_CGROUP_DEVICE",
		BPFProgTypeSkMsg:                 "BPF_PROG_TYPE_SK_MSG",
		BPFProgTypeRawTracepoint:         "BPF_PROG_TYPE_RAW_TRACEPOINT",
		BPFProgTypeCgroupSockAddr:        "BPF_PROG_TYPE_CGROUP_SOCK_ADDR",
		BPFProgTypeLwtSeg6Local:          "BPF_PROG_TYPE_LWT_SEG6LOCAL",
		BPFProgTypeLircMode2:             "BPF_PROG_TYPE_LIRC_MODE2",
		BPFProgTypeSkReuseport:           "BPF_PROG_TYPE_SK_REUSEPORT",
		BPFProgTypeFlowDissector:         "BPF_PROG_TYPE_FLOW_DISSECTOR",
		BPFProgTypeCgroupSysctl:          "BPF_PROG_TYPE_CGROUP_SYSCTL",
		BPFProgTypeRawTracepointWritable: "BPF_PROG_TYPE_RAW_TRACEPOINT_WRITABLE",
		BPFProgTypeCgroupSockopt:         "BPF_PROG_TYPE_CGROUP_SOCKOPT",
		BPFProgTypeTracing:               "BPF_PROG_TYPE_TRACING",
		BPFProgTypeStructOps:             "BPF_PROG_TYPE_STRUCT_OPS",
		BPFProgTypeExt:                   "BPF_PROG_TYPE_EXT",
		BPFProgTypeLsm:                   "BPF_PROG_TYPE_LSM",
		BPFProgTypeSkLookup:              "BPF_PROG_TYPE_SK_LOOKUP",
		BPFProgTypeSyscall:               "BPF_PROG_TYPE_SYSCALL",
	}
	str = x[b]
	if str == "" {
		str = BPFProgTypeUnspec.String()
	}
	return str
}

type BPFAttachType uint32

const (
	BPFAttachTypeCgroupInetIngress BPFAttachType = iota
	BPFAttachTypeCgroupInetEgress
	BPFAttachTypeCgroupInetSockCreate
	BPFAttachTypeCgroupSockOps
	BPFAttachTypeSKSKBStreamParser
	BPFAttachTypeSKSKBStreamVerdict
	BPFAttachTypeCgroupDevice
	BPFAttachTypeSKMSGVerdict
	BPFAttachTypeCgroupInet4Bind
	BPFAttachTypeCgroupInet6Bind
	BPFAttachTypeCgroupInet4Connect
	BPFAttachTypeCgroupInet6Connect
	BPFAttachTypeCgroupInet4PostBind
	BPFAttachTypeCgroupInet6PostBind
	BPFAttachTypeCgroupUDP4SendMsg
	BPFAttachTypeCgroupUDP6SendMsg
	BPFAttachTypeLircMode2
	BPFAttachTypeFlowDissector
	BPFAttachTypeCgroupSysctl
	BPFAttachTypeCgroupUDP4RecvMsg
	BPFAttachTypeCgroupUDP6RecvMsg
	BPFAttachTypeCgroupGetSockOpt
	BPFAttachTypeCgroupSetSockOpt
	BPFAttachTypeTraceRawTP
	BPFAttachTypeTraceFentry
	BPFAttachTypeTraceFexit
	BPFAttachTypeModifyReturn
	BPFAttachTypeLSMMac
	BPFAttachTypeTraceIter
	BPFAttachTypeCgroupInet4GetPeerName
	BPFAttachTypeCgroupInet6GetPeerName
	BPFAttachTypeCgroupInet4GetSockName
	BPFAttachTypeCgroupInet6GetSockName
	BPFAttachTypeXDPDevMap
	BPFAttachTypeCgroupInetSockRelease
	BPFAttachTypeXDPCPUMap
	BPFAttachTypeSKLookup
	BPFAttachTypeXDP
	BPFAttachTypeSKSKBVerdict
	BPFAttachTypeSKReusePortSelect
	BPFAttachTypeSKReusePortSelectorMigrate
	BPFAttachTypePerfEvent
	BPFAttachTypeTraceKprobeMulti
)

func (p *BPFProg) GetType() BPFProgType {
	return BPFProgType(C.bpf_program__get_type(p.prog))
}

func (p *BPFProg) SetAutoload(autoload bool) error {
	cbool := C.bool(autoload)
	ret, errC := C.bpf_program__set_autoload(p.prog, cbool)
	if ret != 0 {
		return fmt.Errorf("failed to set bpf program autoload: %w", errC)
	}
	return nil
}

// AttachGeneric is used to attach the BPF program using autodetection
// for the attach target. You can specify the destination in BPF code
// via the SEC() such as `SEC("fentry/some_kernel_func")`
func (p *BPFProg) AttachGeneric() (*BPFLink, error) {
	link, errno := C.bpf_program__attach(p.prog)
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, fmt.Errorf("failed to attach program: %w", errno)
	}
	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  Tracing,
		eventName: fmt.Sprintf("tracing-%s", p.name),
	}
	return bpfLink, nil
}

// SetAttachTarget can be used to specify the program and/or function to attach
// the BPF program to. To attach to a kernel function specify attachProgFD as 0
func (p *BPFProg) SetAttachTarget(attachProgFD int, attachFuncName string) error {
	cs := C.CString(attachFuncName)
	ret, errC := C.bpf_program__set_attach_target(p.prog, C.int(attachProgFD), cs)
	C.free(unsafe.Pointer(cs))
	if ret != 0 {
		return fmt.Errorf("failed to set attach target for program %s %s %w", p.name, attachFuncName, errC)
	}
	return nil
}

func (p *BPFProg) SetProgramType(progType BPFProgType) {
	C.bpf_program__set_type(p.prog, C.enum_bpf_prog_type(int(progType)))
}

func (p *BPFProg) SetAttachType(attachType BPFAttachType) {
	C.bpf_program__set_expected_attach_type(p.prog, C.enum_bpf_attach_type(int(attachType)))
}

func (p *BPFProg) AttachXDP(deviceName string) (*BPFLink, error) {
	iface, err := net.InterfaceByName(deviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to find device by name %s: %w", deviceName, err)
	}
	link := C.bpf_program__attach_xdp(p.prog, C.int(iface.Index))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach xdp on device %s to program %s", deviceName, p.name)
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  XDP,
		eventName: fmt.Sprintf("xdp-%s-%s", p.name, deviceName),
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func (p *BPFProg) AttachTracepoint(category, name string) (*BPFLink, error) {
	tpCategory := C.CString(category)
	tpName := C.CString(name)
	link := C.bpf_program__attach_tracepoint(p.prog, tpCategory, tpName)
	C.free(unsafe.Pointer(tpCategory))
	C.free(unsafe.Pointer(tpName))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach tracepoint %s to program %s", name, p.name)
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  Tracepoint,
		eventName: name,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func (p *BPFProg) AttachRawTracepoint(tpEvent string) (*BPFLink, error) {
	cs := C.CString(tpEvent)
	link := C.bpf_program__attach_raw_tracepoint(p.prog, cs)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach raw tracepoint %s to program %s", tpEvent, p.name)
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  RawTracepoint,
		eventName: tpEvent,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func (p *BPFProg) AttachPerfEvent(fd int) (*BPFLink, error) {
	link := C.bpf_program__attach_perf_event(p.prog, C.int(fd))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach perf event to program %s", p.name)
	}

	bpfLink := &BPFLink{
		link:     link,
		prog:     p,
		linkType: PerfEvent,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

// this API should be used for kernels > 4.17
func (p *BPFProg) AttachKprobe(kp string) (*BPFLink, error) {
	return doAttachKprobe(p, kp, false)
}

// this API should be used for kernels > 4.17
func (p *BPFProg) AttachKretprobe(kp string) (*BPFLink, error) {
	return doAttachKprobe(p, kp, true)
}

func (p *BPFProg) AttachLSM() (*BPFLink, error) {
	link := C.bpf_program__attach_lsm(p.prog)
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach lsm to program %s", p.name)
	}

	bpfLink := &BPFLink{
		link:     link,
		prog:     p,
		linkType: LSM,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func doAttachKprobe(prog *BPFProg, kp string, isKretprobe bool) (*BPFLink, error) {
	cs := C.CString(kp)
	cbool := C.bool(isKretprobe)
	link := C.bpf_program__attach_kprobe(prog.prog, cbool, cs)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach %s k(ret)probe to program %s", kp, prog.name)
	}

	kpType := Kprobe
	if isKretprobe {
		kpType = Kretprobe
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      prog,
		linkType:  kpType,
		eventName: kp,
	}
	prog.module.links = append(prog.module.links, bpfLink)
	return bpfLink, nil
}

// AttachUprobe attaches the BPFProgram to entry of the symbol in the library or binary at 'path'
// which can be relative or absolute. A pid can be provided to attach to, or -1 can be specified
// to attach to all processes
func (p *BPFProg) AttachUprobe(pid int, path string, offset uint32) (*BPFLink, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return doAttachUprobe(p, false, pid, absPath, offset)
}

// AttachURetprobe attaches the BPFProgram to exit of the symbol in the library or binary at 'path'
// which can be relative or absolute. A pid can be provided to attach to, or -1 can be specified
// to attach to all processes
func (p *BPFProg) AttachURetprobe(pid int, path string, offset uint32) (*BPFLink, error) {

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return doAttachUprobe(p, true, pid, absPath, offset)
}

func doAttachUprobe(prog *BPFProg, isUretprobe bool, pid int, path string, offset uint32) (*BPFLink, error) {
	retCBool := C.bool(isUretprobe)
	pidCint := C.int(pid)
	pathCString := C.CString(path)
	offsetCsizet := C.size_t(offset)

	link := C.bpf_program__attach_uprobe(prog.prog, retCBool, pidCint, pathCString, offsetCsizet)
	C.free(unsafe.Pointer(pathCString))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach u(ret)probe to program %s:%d with pid %d, ", path, offset, pid)
	}

	upType := Uprobe
	if isUretprobe {
		upType = Uretprobe
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      prog,
		linkType:  upType,
		eventName: fmt.Sprintf("%s:%d:%d", path, pid, offset),
	}
	return bpfLink, nil
}

var eventChannels = helpers.NewRWArray(maxEventChannels)

func (m *Module) InitRingBuf(mapName string, eventsChan chan []byte) (*RingBuffer, error) {
	bpfMap, err := m.GetMap(mapName)
	if err != nil {
		return nil, err
	}

	if eventsChan == nil {
		return nil, fmt.Errorf("events channel can not be nil")
	}

	slot := eventChannels.Put(eventsChan)
	if slot == -1 {
		return nil, fmt.Errorf("max ring buffers reached")
	}

	rb := C.init_ring_buf(bpfMap.fd, C.uintptr_t(slot))
	if rb == nil {
		return nil, fmt.Errorf("failed to initialize ring buffer")
	}

	ringBuf := &RingBuffer{
		rb:     rb,
		bpfMap: bpfMap,
		slot:   uint(slot),
	}
	m.ringBufs = append(m.ringBufs, ringBuf)
	return ringBuf, nil
}

func (rb *RingBuffer) Start() {
	rb.stop = make(chan struct{})
	rb.wg.Add(1)
	go rb.poll()
}

func (rb *RingBuffer) Stop() {
	if rb.stop != nil {
		// Tell the poll goroutine that it's time to exit
		close(rb.stop)

		// The event channel should be drained here since the consumer
		// may have stopped at this point. Failure to drain it will
		// result in a deadlock: the channel will fill up and the poll
		// goroutine will block in the callback.
		eventChan := eventChannels.Get(rb.slot).(chan []byte)
		go func() {
			for range eventChan {
			}
		}()

		// Wait for the poll goroutine to exit
		rb.wg.Wait()

		// Close the channel -- this is useful for the consumer but
		// also to terminate the drain goroutine above.
		close(eventChan)

		// This allows Stop() to be called multiple times safely
		rb.stop = nil
	}
}

func (rb *RingBuffer) Close() {
	if rb.closed {
		return
	}
	rb.Stop()
	C.ring_buffer__free(rb.rb)
	eventChannels.Remove(rb.slot)
	rb.closed = true
}

func (rb *RingBuffer) isStopped() bool {
	select {
	case <-rb.stop:
		return true
	default:
		return false
	}
}

func (rb *RingBuffer) poll() error {
	defer rb.wg.Done()

	for {
		err := C.ring_buffer__poll(rb.rb, 300)
		if rb.isStopped() {
			break
		}

		if err < 0 {
			if syscall.Errno(-err) == syscall.EINTR {
				continue
			}
			return fmt.Errorf("error polling ring buffer: %d", err)
		}
	}
	return nil
}

func (m *Module) InitPerfBuf(mapName string, eventsChan chan []byte, lostChan chan uint64, pageCnt int) (*PerfBuffer, error) {
	bpfMap, err := m.GetMap(mapName)
	if err != nil {
		return nil, fmt.Errorf("failed to init perf buffer: %v", err)
	}
	if eventsChan == nil {
		return nil, fmt.Errorf("failed to init perf buffer: events channel can not be nil")
	}

	perfBuf := &PerfBuffer{
		bpfMap:     bpfMap,
		eventsChan: eventsChan,
		lostChan:   lostChan,
	}

	slot := eventChannels.Put(perfBuf)
	if slot == -1 {
		return nil, fmt.Errorf("max number of ring/perf buffers reached")
	}

	pb := C.init_perf_buf(bpfMap.fd, C.int(pageCnt), C.uintptr_t(slot))
	if pb == nil {
		eventChannels.Remove(uint(slot))
		return nil, fmt.Errorf("failed to initialize perf buffer")
	}

	perfBuf.pb = pb
	perfBuf.slot = uint(slot)

	m.perfBufs = append(m.perfBufs, perfBuf)
	return perfBuf, nil
}

func (pb *PerfBuffer) Start() {
	pb.stop = make(chan struct{})
	pb.wg.Add(1)
	go pb.poll()
}

func (pb *PerfBuffer) Stop() {
	if pb.stop != nil {
		// Tell the poll goroutine that it's time to exit
		close(pb.stop)

		// The event and lost channels should be drained here since the consumer
		// may have stopped at this point. Failure to drain it will
		// result in a deadlock: the channel will fill up and the poll
		// goroutine will block in the callback.
		go func() {
			for range pb.eventsChan {
			}

			if pb.lostChan != nil {
				for range pb.lostChan {
				}
			}
		}()

		// Wait for the poll goroutine to exit
		pb.wg.Wait()

		// Close the channel -- this is useful for the consumer but
		// also to terminate the drain goroutine above.
		close(pb.eventsChan)
		if pb.lostChan != nil {
			close(pb.lostChan)
		}

		// This allows Stop() to be called multiple times safely
		pb.stop = nil
	}
}

func (pb *PerfBuffer) Close() {
	if pb.closed {
		return
	}
	pb.Stop()
	C.perf_buffer__free(pb.pb)
	eventChannels.Remove(pb.slot)
	pb.closed = true
}

// todo: consider writing the perf polling in go as c to go calls (callback) are expensive
func (pb *PerfBuffer) poll() error {
	defer pb.wg.Done()

	for {
		select {
		case <-pb.stop:
			return nil
		default:
			err := C.perf_buffer__poll(pb.pb, 300)
			if err < 0 {
				if syscall.Errno(-err) == syscall.EINTR {
					continue
				}
				return fmt.Errorf("error polling perf buffer: %d", err)
			}
		}
	}
}

type TcAttachPoint uint32

const (
	BPFTcIngress       TcAttachPoint = C.BPF_TC_INGRESS
	BPFTcEgress        TcAttachPoint = C.BPF_TC_EGRESS
	BPFTcIngressEgress TcAttachPoint = C.BPF_TC_INGRESS | C.BPF_TC_EGRESS
	BPFTcCustom        TcAttachPoint = C.BPF_TC_CUSTOM
)

type TcFlags uint32

const (
	BpfTcFReplace TcFlags = C.BPF_TC_F_REPLACE
)

type TcHook struct {
	hook *C.struct_bpf_tc_hook
}

type TcOpts struct {
	ProgFd   int
	Flags    TcFlags
	ProgId   uint
	Handle   uint
	Priority uint
}

func tcOptsToC(tcOpts *TcOpts) *C.struct_bpf_tc_opts {
	if tcOpts == nil {
		return nil
	}
	opts := C.struct_bpf_tc_opts{}
	opts.sz = C.sizeof_struct_bpf_tc_opts
	opts.prog_fd = C.int(tcOpts.ProgFd)
	opts.flags = C.uint(tcOpts.Flags)
	opts.prog_id = C.uint(tcOpts.ProgId)
	opts.handle = C.uint(tcOpts.Handle)
	opts.priority = C.uint(tcOpts.Priority)

	return &opts
}

func tcOptsFromC(tcOpts *TcOpts, opts *C.struct_bpf_tc_opts) {
	if opts == nil {
		return
	}
	tcOpts.ProgFd = int(opts.prog_fd)
	tcOpts.Flags = TcFlags(opts.flags)
	tcOpts.ProgId = uint(opts.prog_id)
	tcOpts.Handle = uint(opts.handle)
	tcOpts.Priority = uint(opts.priority)
}

func (m *Module) TcHookInit() *TcHook {
	hook := C.struct_bpf_tc_hook{}
	hook.sz = C.sizeof_struct_bpf_tc_hook

	return &TcHook{
		hook: &hook,
	}
}

func (hook *TcHook) SetInterfaceByIndex(ifaceIdx int) {
	hook.hook.ifindex = C.int(ifaceIdx)
}

func (hook *TcHook) SetInterfaceByName(ifaceName string) error {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return err
	}
	hook.hook.ifindex = C.int(iface.Index)

	return nil
}

func (hook *TcHook) GetInterfaceIndex() int {
	return int(hook.hook.ifindex)
}

func (hook *TcHook) SetAttachPoint(attachPoint TcAttachPoint) {
	hook.hook.attach_point = uint32(attachPoint)
}

func (hook *TcHook) SetParent(a int, b int) {
	parent := (((a) << 16) & 0xFFFF0000) | ((b) & 0x0000FFFF)
	hook.hook.parent = C.uint(parent)
}

func (hook *TcHook) Create() error {
	errC := C.bpf_tc_hook_create(hook.hook)
	if errC < 0 {
		return fmt.Errorf("failed to create tc hook: %w", syscall.Errno(-errC))
	}

	return nil
}

func (hook *TcHook) Destroy() error {
	errC := C.bpf_tc_hook_destroy(hook.hook)
	if errC < 0 {
		return fmt.Errorf("failed to destroy tc hook: %w", syscall.Errno(-errC))
	}

	return nil
}

func (hook *TcHook) Attach(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	errC := C.bpf_tc_attach(hook.hook, opts)
	if errC < 0 {
		return fmt.Errorf("failed to attach tc hook: %w", syscall.Errno(-errC))
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}

func (hook *TcHook) Detach(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	errC := C.bpf_tc_detach(hook.hook, opts)
	if errC < 0 {
		return fmt.Errorf("failed to detach tc hook: %w", syscall.Errno(-errC))
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}

func (hook *TcHook) Query(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	errC := C.bpf_tc_query(hook.hook, opts)
	if errC < 0 {
		return fmt.Errorf("failed to query tc hook: %w", syscall.Errno(-errC))
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}

func BPFMapTypeIsSupported(mapType MapType) (bool, error) {
	cSupported := C.libbpf_probe_bpf_map_type(C.enum_bpf_map_type(int(mapType)), nil)
	if cSupported < 1 {
		return false, syscall.Errno(-cSupported)
	}
	return cSupported == 1, nil
}

func BPFProgramTypeIsSupported(progType BPFProgType) (bool, error) {
	cSupported := C.libbpf_probe_bpf_prog_type(C.enum_bpf_prog_type(int(progType)), nil)
	if cSupported < 1 {
		return false, syscall.Errno(-cSupported)
	}
	return cSupported == 1, nil
}
