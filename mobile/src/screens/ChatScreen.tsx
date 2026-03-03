import React, { useState, useRef, useCallback, useEffect } from 'react';
import {
  View,
  FlatList,
  StyleSheet,
  KeyboardAvoidingView,
  Platform,
  Alert,
  NativeSyntheticEvent,
  NativeScrollEvent,
} from 'react-native';
import type { ChatMessage, PendingMedia, StreamingState, SSEEvent } from '../types';
import { sendMessage, fetchHistory, sendReaction } from '../services/api';
import MessageBubble from '../components/MessageBubble';
import ChatInput from '../components/ChatInput';
import ReplyBar from '../components/ReplyBar';
import ReactionPicker from '../components/ReactionPicker';
import { colors, spacing } from '../theme';

export default function ChatScreen() {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [streaming, setStreaming] = useState<StreamingState>({
    isStreaming: false,
    phase: 'idle',
    accumulatedText: '',
  });
  const [replyTo, setReplyTo] = useState<ChatMessage | null>(null);
  const [pendingMedia, setPendingMedia] = useState<PendingMedia[]>([]);
  const [reactionTarget, setReactionTarget] = useState<string | null>(null);
  const [failedMsgIds, setFailedMsgIds] = useState<Set<string>>(new Set());
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const flatListRef = useRef<FlatList>(null);

  useEffect(() => {
    fetchHistory(50)
      .then((msgs) => {
        setMessages(msgs);
        setHasMore(msgs.length >= 50);
      })
      .catch((e) => Alert.alert('Connection Error', e.message));
  }, []);

  const loadMore = useCallback(async () => {
    if (loadingMore || !hasMore || messages.length === 0) return;
    setLoadingMore(true);
    try {
      const oldest = messages[0];
      const older = await fetchHistory(50, oldest.ts);
      if (older.length === 0) {
        setHasMore(false);
      } else {
        setMessages((prev) => [...older, ...prev]);
        if (older.length < 50) setHasMore(false);
      }
    } catch (e) {
      console.error('Load more error:', e);
    } finally {
      setLoadingMore(false);
    }
  }, [loadingMore, hasMore, messages]);

  const scrollToBottom = useCallback(() => {
    setTimeout(() => flatListRef.current?.scrollToEnd({ animated: true }), 100);
  }, []);

  const doSend = useCallback(
    async (text: string, mediaIds: string[], replyId?: string, retryMsgId?: string) => {
      const msgId = retryMsgId || `temp-${Date.now()}`;

      if (!retryMsgId) {
        const userMsg: ChatMessage = {
          id: msgId,
          role: 'user',
          text: text.trim(),
          ts: new Date().toISOString(),
          reply_to: replyId,
          media: pendingMedia.map((m) => ({
            upload_id: m.upload_id,
            type: m.mime_type.startsWith('image/') ? 'photo' as const : 'document' as const,
            file_name: m.file_name,
            mime_type: m.mime_type,
            url: m.uri,
          })),
        };
        setMessages((prev) => [...prev, userMsg]);
        scrollToBottom();
      } else {
        setFailedMsgIds((prev) => {
          const next = new Set(prev);
          next.delete(retryMsgId);
          return next;
        });
      }

      setStreaming({ isStreaming: true, phase: 'thinking', accumulatedText: '' });

      try {
        let accumulated = '';
        await sendMessage(
          text.trim(),
          {
            reply_to: replyId,
            media_ids: mediaIds.length > 0 ? mediaIds : undefined,
          },
          (event: SSEEvent) => {
            switch (event.type) {
              case 'thinking':
                setStreaming((s) => ({ ...s, phase: 'thinking' }));
                break;
              case 'tool_use':
                setStreaming((s) => ({
                  ...s,
                  phase: 'tool_use',
                  toolName: event.data?.name,
                }));
                break;
              case 'text':
                accumulated += event.data?.text || '';
                setStreaming((s) => ({
                  ...s,
                  phase: 'writing',
                  accumulatedText: accumulated,
                }));
                scrollToBottom();
                break;
              case 'reaction': {
                const emoji = event.data?.emoji;
                if (emoji) {
                  setMessages((prev) =>
                    prev.map((m) =>
                      m.id === msgId
                        ? {
                            ...m,
                            reactions: [...(m.reactions || []), { emoji, from: 'alf' as const }],
                          }
                        : m,
                    ),
                  );
                }
                break;
              }
              case 'done': {
                const data = event.data;
                const assistantMsg: ChatMessage = {
                  id: data.msg_id || `assistant-${Date.now()}`,
                  role: 'assistant',
                  text: accumulated,
                  ts: new Date().toISOString(),
                  model: data.model,
                  tier: data.tier,
                  cost_usd: data.cost_usd,
                  session_id: data.session_id,
                };
                setMessages((prev) => [...prev, assistantMsg]);
                setStreaming({
                  isStreaming: false,
                  phase: 'idle',
                  accumulatedText: '',
                });
                scrollToBottom();
                break;
              }
              case 'error':
                setStreaming({
                  isStreaming: false,
                  phase: 'idle',
                  accumulatedText: '',
                });
                break;
            }
          },
        );
      } catch (e: any) {
        console.error('Send error:', e);
        setStreaming({ isStreaming: false, phase: 'idle', accumulatedText: '' });
        setFailedMsgIds((prev) => new Set(prev).add(msgId));
        Alert.alert('Send Failed', e.message || 'Could not reach ALF');
      }
    },
    [pendingMedia, scrollToBottom],
  );

  const handleSend = useCallback(
    async (text: string) => {
      if (!text.trim() && pendingMedia.length === 0) return;

      const mediaIds = pendingMedia.map((m) => m.upload_id);
      const replyId = replyTo?.id;
      setPendingMedia([]);
      setReplyTo(null);

      await doSend(text, mediaIds, replyId);
    },
    [replyTo, pendingMedia, doSend],
  );

  const handleRetry = useCallback(
    (msg: ChatMessage) => {
      doSend(msg.text, msg.media?.map((m) => m.upload_id) || [], msg.reply_to, msg.id);
    },
    [doSend],
  );

  const handleReaction = useCallback(
    async (msgId: string, emoji: string) => {
      setReactionTarget(null);
      setMessages((prev) =>
        prev.map((m) =>
          m.id === msgId
            ? { ...m, reactions: [...(m.reactions || []), { emoji, from: 'user' as const }] }
            : m,
        ),
      );
      try {
        const result = await sendReaction(msgId, emoji);
        if (result.mirror) {
          setMessages((prev) =>
            prev.map((m) =>
              m.id === msgId
                ? {
                    ...m,
                    reactions: [
                      ...(m.reactions || []),
                      { emoji: result.mirror!, from: 'alf' as const },
                    ],
                  }
                : m,
            ),
          );
        }
      } catch (e) {
        console.error('Reaction error:', e);
      }
    },
    [],
  );

  const handleSwipeReply = useCallback((msg: ChatMessage) => {
    setReplyTo(msg);
  }, []);

  const handleScroll = useCallback(
    (event: NativeSyntheticEvent<NativeScrollEvent>) => {
      if (event.nativeEvent.contentOffset.y < 100) {
        loadMore();
      }
    },
    [loadMore],
  );

  const handleLongPress = useCallback((msgId: string) => {
    setReactionTarget(msgId);
  }, []);

  const renderMessage = useCallback(
    ({ item }: { item: ChatMessage }) => (
      <MessageBubble
        message={item}
        onLongPress={() => handleLongPress(item.id)}
        onSwipeRight={() => handleSwipeReply(item)}
        showError={failedMsgIds.has(item.id)}
        onRetry={() => handleRetry(item)}
      />
    ),
    [handleLongPress, handleSwipeReply, failedMsgIds, handleRetry],
  );

  return (
    <KeyboardAvoidingView
      style={styles.container}
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      keyboardVerticalOffset={90}
    >
      <FlatList
        ref={flatListRef}
        data={messages}
        renderItem={renderMessage}
        keyExtractor={(item) => item.id}
        style={styles.list}
        contentContainerStyle={styles.listContent}
        onContentSizeChange={scrollToBottom}
        onScroll={handleScroll}
        scrollEventThrottle={200}
        ListFooterComponent={
          streaming.isStreaming ? (
            <MessageBubble
              message={{
                id: 'streaming',
                role: 'assistant',
                text: streaming.accumulatedText,
                ts: new Date().toISOString(),
              }}
              isStreaming
              streamingPhase={streaming.phase}
              toolName={streaming.toolName}
            />
          ) : null
        }
      />

      {replyTo && (
        <ReplyBar message={replyTo} onCancel={() => setReplyTo(null)} />
      )}

      <ChatInput
        onSend={handleSend}
        onMediaAttached={(media) => setPendingMedia((prev) => [...prev, media])}
        pendingMedia={pendingMedia}
        onRemoveMedia={(id) =>
          setPendingMedia((prev) => prev.filter((m) => m.upload_id !== id))
        }
        disabled={streaming.isStreaming}
      />

      {reactionTarget && (
        <ReactionPicker
          onSelect={(emoji) => handleReaction(reactionTarget, emoji)}
          onClose={() => setReactionTarget(null)}
        />
      )}
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  list: {
    flex: 1,
  },
  listContent: {
    paddingHorizontal: spacing.md,
    paddingTop: spacing.sm,
    paddingBottom: spacing.sm,
  },
});
