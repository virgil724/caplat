package caplat

import (
	"context"
	"errors"
)

// RunAV measures video (v) and audio (a) at the same time. The video run
// decides when to stop (v.Duration, Esc, ctx); a.Duration is ignored.
// updates receives the video updates; a.OnUpdate the audio ones.
func RunAV(ctx context.Context, v Config, a AudioConfig, updates chan<- Update) (AVResult, error) {
	return runAVWith(ctx, v, a, updates, defaultRunDeps(), defaultAudioDeps())
}

func runAVWith(ctx context.Context, v Config, a AudioConfig, updates chan<- Update, rdeps runDeps, adeps audioDeps) (AVResult, error) {
	actx, stopAudio := context.WithCancel(ctx)
	defer stopAudio()
	a.Duration = 0
	type audioDone struct {
		res AudioResult
		err error
	}
	ach := make(chan audioDone, 1)
	go func() {
		r, err := runAudioWith(actx, a, adeps)
		ach <- audioDone{r, err}
	}()
	vres, verr := runWith(ctx, v, updates, rdeps)
	stopAudio()
	ad := <-ach
	res := AVResult{Video: vres, Audio: ad.res}
	if verr != nil {
		return res, verr
	}
	if ad.err != nil && !errors.Is(ad.err, context.Canceled) {
		return res, ad.err
	}
	return res, nil
}
