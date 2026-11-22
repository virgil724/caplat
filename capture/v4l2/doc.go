// Package v4l2 captures video frames on Linux in-process using V4L2 memory-mapped
// buffers (VIDIOC_REQBUFS / VIDIOC_QBUF / VIDIOC_DQBUF) with zero cgo.
//
// # Zero-Copy Capture with Frame.Hold
//
// By default, Frame.Data and Frame.Y passed to the onFrame callback are only valid
// until onFrame returns, at which point the MMAP buffer is immediately re-queued
// to the kernel via VIDIOC_QBUF.
//
// Consumers that upload Frame.Data asynchronously (such as a GPU render thread)
// can avoid an extra CPU-side copy by calling f.Hold() inside onFrame. Calling
// Hold delays VIDIOC_QBUF for that buffer until the returned release function is
// invoked. To prevent starving the V4L2 driver, Start requests at least 4 MMAP
// buffers and leaves at least 1 buffer queued with the driver at all times; if
// all hold slots are busy when Hold is called, Hold returns nil and increments
// Source.Dropped so the caller can drop or copy the frame rather than blocking
// the driver.
//
// Future capture/mf support can attach the exact same capture.WithHold hook by
// delaying IMFMediaBuffer::Unlock and sample release until release is called.
//
// # Timestamps and uvcvideo
//
// V4L2 frames carry CLOCK_MONOTONIC timestamps in Frame.QPC (arrival at
// VIDIOC_DQBUF return) and Frame.DeviceQPC (from v4l2_buffer.timestamp when
// V4L2_BUF_FLAG_TIMESTAMP_MONOTONIC is set). By default uvcvideo marks SOE
// (start-of-exposure / first USB packet). If uvcvideo.hwtimestamps=1 is enabled
// and firmware PTS jumps ahead of CLOCK_MONOTONIC, DeviceQPC is clamped to 0.
package v4l2
