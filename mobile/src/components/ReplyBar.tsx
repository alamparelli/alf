import React from 'react';
import { View, Text, TouchableOpacity, StyleSheet } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import type { ChatMessage } from '../types';
import { colors, spacing, radius, typography } from '../theme';

interface Props {
  message: ChatMessage;
  onCancel: () => void;
}

export default function ReplyBar({ message, onCancel }: Props) {
  const preview =
    message.text.length > 80
      ? message.text.slice(0, 80) + '...'
      : message.text;

  const isUser = message.role === 'user';

  return (
    <View style={styles.container}>
      <View style={styles.accent} />
      <View style={styles.content}>
        <Text style={styles.label}>
          {isUser ? 'You' : 'ALF'}
        </Text>
        <Text style={styles.preview} numberOfLines={1}>
          {preview}
        </Text>
      </View>
      <TouchableOpacity
        style={styles.closeBtn}
        onPress={onCancel}
        activeOpacity={0.6}
        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
      >
        <Ionicons name="close" size={16} color={colors.textTertiary} />
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: colors.bg,
    borderTopWidth: 0.5,
    borderTopColor: colors.separator,
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.sm,
  },
  accent: {
    width: 2,
    height: 32,
    backgroundColor: colors.accent,
    borderRadius: 1,
    marginRight: spacing.md,
  },
  content: {
    flex: 1,
    gap: 1,
  },
  label: {
    ...typography.footnote,
    color: colors.accent,
    fontWeight: '600',
  },
  preview: {
    ...typography.footnote,
    color: colors.textTertiary,
  },
  closeBtn: {
    width: 28,
    height: 28,
    justifyContent: 'center',
    alignItems: 'center',
    borderRadius: radius.full,
  },
});
