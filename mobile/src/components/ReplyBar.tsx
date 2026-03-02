import React from 'react';
import { View, Text, TouchableOpacity, StyleSheet } from 'react-native';
import type { ChatMessage } from '../types';

interface Props {
  message: ChatMessage;
  onCancel: () => void;
}

export default function ReplyBar({ message, onCancel }: Props) {
  const preview =
    message.text.length > 80
      ? message.text.slice(0, 80) + '...'
      : message.text;

  return (
    <View style={styles.container}>
      <View style={styles.bar} />
      <View style={styles.content}>
        <Text style={styles.label}>
          Replying to {message.role === 'user' ? 'yourself' : 'ALF'}
        </Text>
        <Text style={styles.preview} numberOfLines={1}>
          {preview}
        </Text>
      </View>
      <TouchableOpacity style={styles.closeBtn} onPress={onCancel}>
        <Text style={styles.closeText}>x</Text>
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#1a1a2e',
    borderTopWidth: 1,
    borderTopColor: '#2a2a4e',
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  bar: {
    width: 3,
    height: '100%',
    backgroundColor: '#6c63ff',
    borderRadius: 2,
    marginRight: 10,
  },
  content: {
    flex: 1,
  },
  label: {
    color: '#6c63ff',
    fontSize: 12,
    fontWeight: '600',
  },
  preview: {
    color: '#888',
    fontSize: 14,
    marginTop: 2,
  },
  closeBtn: {
    width: 28,
    height: 28,
    justifyContent: 'center',
    alignItems: 'center',
  },
  closeText: {
    color: '#888',
    fontSize: 18,
  },
});
