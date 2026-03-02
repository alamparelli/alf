import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Image,
} from 'react-native';
import type { ChatMessage } from '../types';

interface Props {
  message: ChatMessage;
  isStreaming?: boolean;
  streamingPhase?: string;
  toolName?: string;
  onLongPress?: () => void;
  onSwipeRight?: () => void;
}

export default function MessageBubble({
  message,
  isStreaming,
  streamingPhase,
  toolName,
  onLongPress,
}: Props) {
  const isUser = message.role === 'user';

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

  return (
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
        ]}
      >
        {renderMedia()}
        {renderStatus()}
        {message.text ? (
          <Text style={[styles.text, isUser ? styles.userText : styles.assistantText]}>
            {message.text}
          </Text>
        ) : null}
        {message.model && !isUser && !isStreaming && (
          <Text style={styles.meta}>
            {message.tier || message.model}
            {message.cost_usd ? ` · $${message.cost_usd.toFixed(4)}` : ''}
          </Text>
        )}
        {renderReactions()}
      </View>
    </TouchableOpacity>
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
});
