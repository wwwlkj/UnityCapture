package main

import (
    "log"
    "syscall"
    "time"
    "unsafe"
)

type sharedHeader struct {
    MaxSize    uint32
    Width      int32
    Height     int32
    Stride     int32
    Format     int32
    ResizeMode int32
    MirrorMode int32
    Timeout    int32
    Data       [1]byte // first byte of pixel data
}

type sender struct {
    mutex     syscall.Handle
    wantEvent syscall.Handle
    sentEvent syscall.Handle
    mapping   syscall.Handle
    buf       []byte
    header    *sharedHeader
}

const (
    SYNCHRONIZE        = 0x00100000
    EVENT_MODIFY_STATE = 0x0002
    FILE_MAP_WRITE     = 0x0002
    PAGE_READWRITE     = 0x04
    WAIT_OBJECT_0      = 0
)

var (
    kernel            = syscall.NewLazyDLL("kernel32.dll")
    procOpenMutexA    = kernel.NewProc("OpenMutexA")
    procOpenEventA    = kernel.NewProc("OpenEventA")
    procOpenFileMapA  = kernel.NewProc("OpenFileMappingA")
    procMapViewOfFile = kernel.NewProc("MapViewOfFile")
    procCloseHandle   = kernel.NewProc("CloseHandle")
    procReleaseMutex  = kernel.NewProc("ReleaseMutex")
    procWaitForSingle = kernel.NewProc("WaitForSingleObject")
    procSetEvent      = kernel.NewProc("SetEvent")
)

func openSender() (*sender, error) {
    s := &sender{}
    mutexName := []byte("UnityCapture_Mutx" + string(byte(0)) + "\x00")
    wantName := []byte("UnityCapture_Want" + string(byte(0)) + "\x00")
    sentName := []byte("UnityCapture_Sent" + string(byte(0)) + "\x00")
    mapName := []byte("UnityCapture_Data" + string(byte(0)) + "\x00")

    r0, _, err := procOpenMutexA.Call(SYNCHRONIZE, 0, uintptr(unsafe.Pointer(&mutexName[0])))
    if r0 == 0 {
        return nil, err
    }
    s.mutex = syscall.Handle(r0)

    r0, _, err = procOpenEventA.Call(EVENT_MODIFY_STATE, 0, uintptr(unsafe.Pointer(&wantName[0])))
    if r0 == 0 {
        s.Close()
        return nil, err
    }
    s.wantEvent = syscall.Handle(r0)

    r0, _, err = procOpenEventA.Call(EVENT_MODIFY_STATE, 0, uintptr(unsafe.Pointer(&sentName[0])))
    if r0 == 0 {
        s.Close()
        return nil, err
    }
    s.sentEvent = syscall.Handle(r0)

    r0, _, err = procOpenFileMapA.Call(FILE_MAP_WRITE, 0, uintptr(unsafe.Pointer(&mapName[0])))
    if r0 == 0 {
        s.Close()
        return nil, err
    }
    s.mapping = syscall.Handle(r0)

    // map entire file
    r0, _, err = procMapViewOfFile.Call(uintptr(s.mapping), FILE_MAP_WRITE, 0, 0, 0)
    if r0 == 0 {
        s.Close()
        return nil, err
    }
    s.buf = (*[1 << 30]byte)(unsafe.Pointer(r0))[:]
    s.header = (*sharedHeader)(unsafe.Pointer(&s.buf[0]))
    return s, nil
}

func (s *sender) Close() {
    if s.buf != nil {
        syscall.UnmapViewOfFile(uintptr(unsafe.Pointer(&s.buf[0])))
        s.buf = nil
    }
    if s.mapping != 0 {
        procCloseHandle.Call(uintptr(s.mapping))
        s.mapping = 0
    }
    if s.sentEvent != 0 {
        procCloseHandle.Call(uintptr(s.sentEvent))
        s.sentEvent = 0
    }
    if s.wantEvent != 0 {
        procCloseHandle.Call(uintptr(s.wantEvent))
        s.wantEvent = 0
    }
    if s.mutex != 0 {
        procCloseHandle.Call(uintptr(s.mutex))
        s.mutex = 0
    }
}

func (s *sender) send(width, height int, frame []byte) error {
    stride := width * 4
    hdr := s.header
    _, _, _ = procWaitForSingle.Call(uintptr(s.mutex), syscall.INFINITE)
    hdr.Width = int32(width)
    hdr.Height = int32(height)
    hdr.Stride = int32(stride)
    hdr.Format = 0 // uint8
    hdr.ResizeMode = 0
    hdr.MirrorMode = 0
    hdr.Timeout = 1000
    copy(s.buf[unsafe.Offsetof(hdr.Data):], frame)
    procReleaseMutex.Call(uintptr(s.mutex))
    procSetEvent.Call(uintptr(s.sentEvent))
    return nil
}

func main() {
    s, err := openSender()
    if err != nil {
        log.Fatalf("open shared memory: %v", err)
    }
    defer s.Close()

    width, height := 640, 480
    frame := make([]byte, width*height*4)
    for y := 0; y < height; y++ {
        for x := 0; x < width; x++ {
            idx := (y*width + x) * 4
            frame[idx+0] = byte(x * 255 / width)
            frame[idx+1] = byte(y * 255 / height)
            frame[idx+2] = 0
            frame[idx+3] = 255
        }
    }
    for i := 0; i < 300; i++ {
        if err := s.send(width, height, frame); err != nil {
            log.Printf("send error: %v", err)
        }
        time.Sleep(33 * time.Millisecond)
    }
}

