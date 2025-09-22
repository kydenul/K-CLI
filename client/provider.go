package client

import "github.com/kydenul/log"

const (
	ModelClaude3     = "claude-3"
	ModelDeepSeekR1  = "deepseek-r1"
	ModelDeepSeekV31 = "DeepSeek-V3_1"
)

type BaseProvider struct{ log.Logger }

func (p *BaseProvider) CallStreamableChatCompletions(_ []*Message, _ *string) *Message {
	p.Panicf("Not implemented")
	return nil
}

func (p *BaseProvider) HandleStreamableChat(streamCh <-chan StreamChunk) LLMStreamRet {
	// NOTE Check channel is closed or not
	chunk, ok := <-streamCh
	if !ok {
		p.Info("Stream channel closed immediately")
		return LLMStreamRet{}
	}

	// NOTE: 1. Error is occurred => return
	if chunk.Error != nil {
		p.Errorln("Stream error:", chunk.Error)
		return LLMStreamRet{Err: chunk.Error}
	}

	// NOTE: 2. Process Done => return
	if chunk.Done {
		p.Info("Stream completed immediately")
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,
			Done:  true,
		}
	}

	// NOTE: 3. Processing
	if chunk.Content != "" {
		p.Infof("Received first chunk: %s", chunk.Content)
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Content:  chunk.Content,
			StreamCh: streamCh,
		}
	}

	// NOTE: 4. Empty first chunk
	p.Info("Empty first chunk, waiting for next")
	return p.waitForNextChunk(streamCh)
}

func (p *BaseProvider) waitForNextChunk(streamCh <-chan StreamChunk) LLMStreamRet {
	// NOTE Check channel is closed or not
	chunk, ok := <-streamCh
	if !ok {
		p.Info("Stream channel closed => complete!!!")
		return LLMStreamRet{}
	}

	if chunk.Error != nil {
		p.Errorln("Stream error:", chunk.Error)
		return LLMStreamRet{Err: chunk.Error}
	}

	if chunk.Done {
		p.Info("Stream completed")
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Done: true,
		}
	}

	if chunk.Content != "" {
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Content:  chunk.Content,
			StreamCh: streamCh,
		}
	}

	p.Info("Empty chunk, waiting for next")
	return p.waitForNextChunk(streamCh)
}
