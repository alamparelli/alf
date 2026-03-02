import React, { useCallback } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Image,
  Animated,
  Platform,
} from 'react-native';
import {
  GestureHandlerRootView,
  PanGestureHandler,
  State,
} from 'react-native-gesture-handler';
import Markdown from 'react-native-markdown-display';
import type { ChatMessage } from '../types';

interface Props {
  message: ChatMessage;
  isStreaming?: boolean;
  streamingPhase?: string;
  toolName?: string;
  onLongPress?: () => void;
  onSwipeRight?: () => void;
  showError?: boolean;
  onRetry?: () => void;
}

const SWIPE_THRESHOLD = 60;

export default function MessageBubble({
  message,
  isStreaming,
  streamingPhase,
  toolName,
  onLongPress,
  onSwipeRight,
  showError,
  onRetry,
}: Props) {
  const isUser = message.role === 'user';
  const translateX = new Animated.Value(0);

  const onGestureEvent = Animated.event(
    [{ nativeEvent: { translationX: translateX } }],
    { useNativeDriver: true },
  );

  const onHandlerStateChange = useCallback(
    (event: any) => {
      if (event.nativeEvent.state === State.END) {
        if (event.nativeEvent.translationX > SWIPE_THRESHOLD && onSwipeRight) {
          onSwipeRight();
        }
        Animated.spring(translateX, {
          toValue: 0,
          useNativeDriver: true,
          tension: 40,
          friction: 8,
        }).start();
      }
    },
    [onSwipeRight, translateX],
  );

  const renderStatus = () => {
    if (!isStreaming) return null;
    if (!message.text && streamingPhase === 'thinking') {
      return <Text style={styles.status}>Thinking...</Text>;
    }
    if (!message.text && streamingPhase === 'tool_use') {
      return <Text style={styles.status}>Using {toolName || 'tool'}...</Text>;
    }
    return null;
  };

  const renderMedia = () => {
    if (!message.media?.length) return null;
    return (
      <View style={styles.mediaContainer}>
        {message.media.map((m) => {
          if (m.mime_type?.startsWith('image/') && m.url) {
            return (
              <Image
                key={m.upload_id}
                source={{ uri: m.url }}
                style={styles.mediaImage}
                resizeMode="cover"
              />
            );
          }
          return (
            <View key={m.upload_id} style={styles.fileChip}>
              <Text style={styles.fileChipText}>{m.file_name}</Text>
            </View>
          );
        })}
      </View>
    );
  };

  const renderReactions = () => {
    if (!message.reactions?.length) return null;
    return (
      <View style={styles.reactions}>
        {message.reactions.map((r, i) => (
          <Text key={i} style={styles.reactionEmoji}>
            {r.emoji}
          </Text>
        ))}
      </View>
    );
  };

  const bubbleContent = (
    <TouchableOpacity
      activeOpacity={0.8}
      onLongPress={onLongPress}
      delayLongPress={300}
      style={[styles.row, isUser ? styles.rowUser : styles.rowAssistant]}
    >
      <View
        style={[
          styles.bubble,
          isUser ? styles.userBubble : styles.assistantBubble,
          showError && styles.errorBubble,
        ]}
      >
        {renderMedia()}
        {renderStatus()}
        {message.text ? (
          isUser ? (
            <Text style={[styles.text, styles.userText]}>{message.text}</Text>
          ) : (
            <Markdown style={markdownStyles}>{message.text}</Markdown>
          )
        ) : null}
        {message.model && !isUser && !isStreaming && (
          <Text style={styles.meta}>
            {message.tier || message.model}
            {message.cost_usd ? ` · $${message.cost_usd.toFixed(4)}` : ''}
          </Text>
        )}
        {renderReactions()}
        {showError && (
          <TouchableOpacity onPress={onRetry} style={styles.retryBtn}>
            <Text style={styles.retryText}>Failed to send. Tap to retry.</Text>
          </TouchableOpacity>
        )}
      </View>
    </TouchableOpacity>
  );

  if (!onSwipeRight || isStreaming) return bubbleContent;

  return (
    <GestureHandlerRootView>
      <PanGestureHandler
        onGestureEvent={onGestureEvent}
        onHandlerStateChange={onHandlerStateChange}
        activeOffsetX={20}
        failOffsetY={[-10, 10]}
      >
        <Animated.View
          style={{
            transform: [
              {
                translateX: translateX.interpolate({
                  inputRange: [0, SWIPE_THRESHOLD * 1.5],
                  outputRange: [0, SWIPE_THRESHOLD],
                  extrapolate: 'clamp',
                }),
              },
            ],
          }}
        >
          {bubbleContent}
        </Animated.View>
      </PanGestureHandler>
    </GestureHandlerRootView>
  );
}

const styles = StyleSheet.create({
  row: {
    marginVertical: 4,
    flexDirection: 'row',
  },
  rowUser: {
    justifyContent: 'flex-end',
  },
  rowAssistant: {
    justifyContent: 'flex-start',
  },
  bubble: {
    maxWidth: '80%',
    borderRadius: 16,
    paddingHorizontal: 14,
    paddingVertical: 10,
  },
  userBubble: {
    backgroundColor: '#6c63ff',
    borderBottomRightRadius: 4,
  },
  assistantBubble: {
    backgroundColor: '#2a2a4e',
    borderBottomLeftRadius: 4,
  },
  errorBubble: {
    borderColor: '#ff4444',
    borderWidth: 1,
  },
  text: {
    fontSize: 16,
    lineHeight: 22,
  },
  userText: {
    color: '#fff',
  },
  assistantText: {
    color: '#e0e0e0',
  },
  status: {
    color: '#999',
    fontSize: 14,
    fontStyle: 'italic',
    marginBottom: 4,
  },
  meta: {
    color: '#666',
    fontSize: 11,
    marginTop: 6,
  },
  mediaContainer: {
    marginBottom: 8,
  },
  mediaImage: {
    width: '100%',
    height: 200,
    borderRadius: 8,
    marginBottom: 4,
  },
  fileChip: {
    backgroundColor: 'rgba(255,255,255,0.1)',
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 6,
    marginBottom: 4,
  },
  fileChipText: {
    color: '#ccc',
    fontSize: 13,
  },
  reactions: {
    flexDirection: 'row',
    marginTop: 6,
    gap: 4,
  },
  reactionEmoji: {
    fontSize: 18,
  },
  retryBtn: {
    marginTop: 6,
  },
  retryText: {
    color: '#ff6666',
    fontSize: 12,
  },
});

const markdownStyles = {
  body: { color: '#e0e0e0', fontSize: 16, lineHeight: 22 },
  heading1: { color: '#fff', fontSize: 22, fontWeight: '700' as const, marginVertical: 6 },
  heading2: { color: '#fff', fontSize: 19, fontWeight: '700' as const, marginVertical: 5 },
  heading3: { color: '#fff', fontSize: 17, fontWeight: '600' as const, marginVertical: 4 },
  strong: { color: '#fff', fontWeight: '700' as const },
  em: { color: '#ccc', fontStyle: 'italic' as const },
  link: { color: '#8b85ff' },
  blockquote: {
    borderLeftWidth: 3,
    borderLeftColor: '#6c63ff',
    paddingLeft: 10,
    marginVertical: 6,
    backgroundColor: 'rgba(108,99,255,0.08)',
    borderRadius: 4,
  },
  code_inline: {
    backgroundColor: 'rgba(255,255,255,0.1)',
    color: '#ffa657',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 14,
    paddingHorizontal: 4,
    borderRadius: 3,
  },
  code_block: {
    backgroundColor: 'rgba(0,0,0,0.3)',
    color: '#e0e0e0',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 13,
    padding: 10,
    borderRadius: 8,
    marginVertical: 6,
  },
  fence: {
    backgroundColor: 'rgba(0,0,0,0.3)',
    color: '#e0e0e0',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 13,
    padding: 10,
    borderRadius: 8,
    marginVertical: 6,
  },
  bullet_list: { marginVertical: 4 },
  ordered_list: { marginVertical: 4 },
  list_item: { color: '#e0e0e0', marginVertical: 2 },
  hr: { backgroundColor: '#444', height: 1, marginVertical: 8 },
  paragraph: { marginVertical: 2 },
};
