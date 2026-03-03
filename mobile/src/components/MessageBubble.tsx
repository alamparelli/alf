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
import { Ionicons } from '@expo/vector-icons';
import Markdown from 'react-native-markdown-display';
import type { ChatMessage } from '../types';
import { colors, spacing, radius, typography } from '../theme';

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
    if (!message.text && (streamingPhase === 'thinking' || streamingPhase === 'tool_use')) {
      const label = streamingPhase === 'thinking'
        ? 'Thinking'
        : `Using ${toolName || 'tool'}`;
      return (
        <View style={styles.statusRow}>
          <View style={styles.statusDot} />
          <Text style={styles.statusText}>{label}</Text>
        </View>
      );
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
              <Ionicons
                name="document-outline"
                size={14}
                color={isUser ? 'rgba(255,255,255,0.7)' : colors.textSecondary}
              />
              <Text
                style={[styles.fileChipText, isUser && styles.fileChipTextUser]}
                numberOfLines={1}
              >
                {m.file_name}
              </Text>
            </View>
          );
        })}
      </View>
    );
  };

  const renderReactions = () => {
    if (!message.reactions?.length) return null;
    return (
      <View style={[styles.reactions, isUser ? styles.reactionsUser : styles.reactionsAssistant]}>
        {message.reactions.map((r, i) => (
          <View key={i} style={styles.reactionBadge}>
            <Text style={styles.reactionEmoji}>{r.emoji}</Text>
          </View>
        ))}
      </View>
    );
  };

  const bubbleContent = (
    <TouchableOpacity
      activeOpacity={0.7}
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
            <Text style={styles.userText}>{message.text}</Text>
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
        {showError && (
          <TouchableOpacity onPress={onRetry} style={styles.retryRow}>
            <Ionicons name="refresh" size={12} color={colors.destructive} />
            <Text style={styles.retryText}>Tap to retry</Text>
          </TouchableOpacity>
        )}
      </View>
      {renderReactions()}
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
    marginVertical: 2,
    flexDirection: 'column',
  },
  rowUser: {
    alignItems: 'flex-end',
  },
  rowAssistant: {
    alignItems: 'flex-start',
  },
  bubble: {
    maxWidth: '82%',
    borderRadius: radius.xl,
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.md,
  },
  userBubble: {
    backgroundColor: colors.userBubble,
    borderBottomRightRadius: spacing.xs,
  },
  assistantBubble: {
    backgroundColor: colors.assistantBubble,
    borderBottomLeftRadius: spacing.xs,
  },
  errorBubble: {
    borderColor: colors.destructive,
    borderWidth: 1,
  },
  userText: {
    ...typography.body,
    color: '#FFFFFF',
    lineHeight: 22,
  },
  statusRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: spacing.sm,
    paddingVertical: 2,
  },
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: colors.accent,
    opacity: 0.8,
  },
  statusText: {
    ...typography.subhead,
    color: colors.textSecondary,
    fontStyle: 'italic',
  },
  meta: {
    ...typography.caption,
    marginTop: spacing.sm,
  },
  mediaContainer: {
    marginBottom: spacing.sm,
    gap: spacing.xs,
  },
  mediaImage: {
    width: '100%',
    height: 200,
    borderRadius: radius.md,
  },
  fileChip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: spacing.xs,
    backgroundColor: 'rgba(255,255,255,0.08)',
    borderRadius: radius.sm,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.sm,
  },
  fileChipText: {
    ...typography.footnote,
    color: colors.textSecondary,
    flexShrink: 1,
  },
  fileChipTextUser: {
    color: 'rgba(255,255,255,0.7)',
  },
  reactions: {
    flexDirection: 'row',
    marginTop: -4,
    gap: 2,
    paddingHorizontal: spacing.sm,
  },
  reactionsUser: {
    justifyContent: 'flex-end',
  },
  reactionsAssistant: {
    justifyContent: 'flex-start',
  },
  reactionBadge: {
    backgroundColor: colors.surface,
    borderRadius: radius.full,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderWidth: 0.5,
    borderColor: colors.separator,
  },
  reactionEmoji: {
    fontSize: 14,
  },
  retryRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: spacing.xs,
    marginTop: spacing.sm,
  },
  retryText: {
    ...typography.caption,
    color: colors.destructive,
  },
});

const markdownStyles = {
  body: {
    color: colors.textPrimary,
    fontSize: 17,
    lineHeight: 24,
    letterSpacing: -0.41,
  },
  heading1: {
    color: colors.textPrimary,
    fontSize: 22,
    fontWeight: '700' as const,
    marginTop: spacing.md,
    marginBottom: spacing.sm,
    letterSpacing: 0.35,
  },
  heading2: {
    color: colors.textPrimary,
    fontSize: 19,
    fontWeight: '700' as const,
    marginTop: spacing.md,
    marginBottom: spacing.xs,
    letterSpacing: -0.41,
  },
  heading3: {
    color: colors.textPrimary,
    fontSize: 17,
    fontWeight: '600' as const,
    marginTop: spacing.sm,
    marginBottom: spacing.xs,
  },
  strong: {
    color: colors.textPrimary,
    fontWeight: '600' as const,
  },
  em: {
    color: 'rgba(255,255,255,0.8)',
    fontStyle: 'italic' as const,
  },
  link: {
    color: colors.accent,
    textDecorationLine: 'none' as const,
  },
  blockquote: {
    borderLeftWidth: 2,
    borderLeftColor: colors.textTertiary,
    paddingLeft: spacing.md,
    marginVertical: spacing.sm,
    opacity: 0.85,
  },
  code_inline: {
    backgroundColor: 'rgba(255,255,255,0.08)',
    color: '#FF9F0A',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 15,
    paddingHorizontal: 5,
    paddingVertical: 1,
    borderRadius: 4,
  },
  code_block: {
    backgroundColor: 'rgba(0,0,0,0.4)',
    color: '#E5E5EA',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 14,
    padding: spacing.md,
    borderRadius: radius.sm,
    marginVertical: spacing.sm,
  },
  fence: {
    backgroundColor: 'rgba(0,0,0,0.4)',
    color: '#E5E5EA',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 14,
    padding: spacing.md,
    borderRadius: radius.sm,
    marginVertical: spacing.sm,
  },
  bullet_list: { marginVertical: spacing.xs },
  ordered_list: { marginVertical: spacing.xs },
  list_item: { color: colors.textPrimary, marginVertical: 1 },
  hr: { backgroundColor: colors.separator, height: 0.5, marginVertical: spacing.md },
  paragraph: { marginVertical: 2 },
};
