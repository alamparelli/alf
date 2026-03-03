import React, { useState, useRef } from 'react';
import {
  View,
  TextInput,
  TouchableOpacity,
  Text,
  StyleSheet,
  ScrollView,
  Image,
  Alert,
  ActionSheetIOS,
  Platform,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Audio } from 'expo-av';
import type { PendingMedia } from '../types';
import { pickPhoto, pickDocument, pickVideo, uploadVoice } from '../services/media';
import { colors, spacing, radius, typography } from '../theme';

interface Props {
  onSend: (text: string) => void;
  onMediaAttached: (media: PendingMedia) => void;
  pendingMedia: PendingMedia[];
  onRemoveMedia: (uploadId: string) => void;
  disabled: boolean;
}

export default function ChatInput({
  onSend,
  onMediaAttached,
  pendingMedia,
  onRemoveMedia,
  disabled,
}: Props) {
  const [text, setText] = useState('');
  const [isRecording, setIsRecording] = useState(false);
  const recordingRef = useRef<Audio.Recording | null>(null);
  const inputRef = useRef<TextInput>(null);

  const handleSend = () => {
    if (text.trim() || pendingMedia.length > 0) {
      onSend(text);
      setText('');
    }
  };

  const handlePickPhoto = async () => {
    try {
      const media = await pickPhoto();
      if (media) onMediaAttached(media);
    } catch (e: any) {
      Alert.alert('Error', e.message || 'Failed to pick photo');
    }
  };

  const handlePickVideo = async () => {
    try {
      const media = await pickVideo();
      if (media) onMediaAttached(media);
    } catch (e: any) {
      Alert.alert('Error', e.message || 'Failed to pick video');
    }
  };

  const handlePickDocument = async () => {
    try {
      const media = await pickDocument();
      if (media) onMediaAttached(media);
    } catch (e: any) {
      Alert.alert('Error', e.message || 'Failed to pick document');
    }
  };

  const handleAttach = () => {
    if (Platform.OS === 'ios') {
      ActionSheetIOS.showActionSheetWithOptions(
        {
          options: ['Cancel', 'Photo', 'Video', 'Document'],
          cancelButtonIndex: 0,
        },
        (index) => {
          if (index === 1) handlePickPhoto();
          else if (index === 2) handlePickVideo();
          else if (index === 3) handlePickDocument();
        },
      );
    } else {
      // Android fallback — show options inline
      Alert.alert('Attach', undefined, [
        { text: 'Photo', onPress: handlePickPhoto },
        { text: 'Video', onPress: handlePickVideo },
        { text: 'Document', onPress: handlePickDocument },
        { text: 'Cancel', style: 'cancel' },
      ]);
    }
  };

  const handleStartRecording = async () => {
    try {
      const permission = await Audio.requestPermissionsAsync();
      if (!permission.granted) return;

      await Audio.setAudioModeAsync({
        allowsRecordingIOS: true,
        playsInSilentModeIOS: true,
      });

      const { recording } = await Audio.Recording.createAsync(
        Audio.RecordingOptionsPresets.HIGH_QUALITY,
      );
      recordingRef.current = recording;
      setIsRecording(true);
    } catch (e: any) {
      Alert.alert('Error', e.message || 'Failed to start recording');
    }
  };

  const handleStopRecording = async () => {
    if (!recordingRef.current) return;
    setIsRecording(false);

    try {
      await recordingRef.current.stopAndUnloadAsync();
      const uri = recordingRef.current.getURI();
      recordingRef.current = null;

      if (uri) {
        const result = await uploadVoice(uri);
        onMediaAttached({
          upload_id: result.upload_id,
          file_name: result.file_name,
          mime_type: result.mime_type,
          uri,
        });
        const label = result.transcript || '[Voice message]';
        onSend(label);
      }
    } catch (e: any) {
      Alert.alert('Error', e.message || 'Failed to process recording');
    }
  };

  const hasContent = text.trim().length > 0 || pendingMedia.length > 0;

  return (
    <View style={styles.container}>
      {pendingMedia.length > 0 && (
        <ScrollView horizontal style={styles.mediaRow} showsHorizontalScrollIndicator={false}>
          {pendingMedia.map((m) => (
            <View key={m.upload_id} style={styles.mediaPreview}>
              {m.mime_type.startsWith('image/') ? (
                <Image source={{ uri: m.uri }} style={styles.previewImage} />
              ) : (
                <View style={styles.previewFile}>
                  <Ionicons name="document" size={18} color={colors.textTertiary} />
                  <Text style={styles.previewFileText} numberOfLines={1}>
                    {m.file_name}
                  </Text>
                </View>
              )}
              <TouchableOpacity
                style={styles.removeBtn}
                onPress={() => onRemoveMedia(m.upload_id)}
              >
                <Ionicons name="close" size={12} color={colors.textPrimary} />
              </TouchableOpacity>
            </View>
          ))}
        </ScrollView>
      )}

      <View style={styles.inputRow}>
        <TouchableOpacity
          style={styles.attachBtn}
          onPress={handleAttach}
          disabled={disabled}
          activeOpacity={0.6}
        >
          <Ionicons
            name="add"
            size={24}
            color={disabled ? colors.textTertiary : colors.textSecondary}
          />
        </TouchableOpacity>

        <View style={styles.inputWrapper}>
          <TextInput
            ref={inputRef}
            style={styles.input}
            value={text}
            onChangeText={setText}
            placeholder="Message"
            placeholderTextColor={colors.textTertiary}
            multiline
            maxLength={4096}
            editable={!disabled}
            onSubmitEditing={handleSend}
            blurOnSubmit={false}
          />
        </View>

        {hasContent ? (
          <TouchableOpacity
            style={[styles.sendBtn, disabled && styles.btnDisabled]}
            onPress={handleSend}
            disabled={disabled}
            activeOpacity={0.6}
          >
            <Ionicons name="arrow-up" size={20} color="#FFFFFF" />
          </TouchableOpacity>
        ) : (
          <TouchableOpacity
            style={[styles.micBtn, isRecording && styles.micActive]}
            onPressIn={handleStartRecording}
            onPressOut={handleStopRecording}
            disabled={disabled}
            activeOpacity={0.6}
          >
            <Ionicons
              name="mic"
              size={20}
              color={isRecording ? '#FFFFFF' : colors.textSecondary}
            />
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    backgroundColor: colors.bg,
    borderTopWidth: 0.5,
    borderTopColor: colors.separator,
    paddingBottom: spacing.xs,
  },
  mediaRow: {
    paddingHorizontal: spacing.md,
    paddingTop: spacing.sm,
    paddingBottom: spacing.xs,
  },
  mediaPreview: {
    marginRight: spacing.sm,
    position: 'relative',
  },
  previewImage: {
    width: 56,
    height: 56,
    borderRadius: radius.sm,
  },
  previewFile: {
    width: 80,
    height: 56,
    borderRadius: radius.sm,
    backgroundColor: colors.surface,
    justifyContent: 'center',
    alignItems: 'center',
    gap: spacing.xs,
    padding: spacing.xs,
  },
  previewFileText: {
    ...typography.caption,
    color: colors.textSecondary,
  },
  removeBtn: {
    position: 'absolute',
    top: -6,
    right: -6,
    backgroundColor: colors.surfaceHover,
    borderRadius: radius.full,
    width: 20,
    height: 20,
    justifyContent: 'center',
    alignItems: 'center',
    borderWidth: 1,
    borderColor: colors.bg,
  },
  inputRow: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    paddingHorizontal: spacing.sm,
    paddingTop: spacing.sm,
    gap: spacing.xs,
  },
  attachBtn: {
    width: 36,
    height: 36,
    borderRadius: radius.full,
    backgroundColor: colors.surface,
    justifyContent: 'center',
    alignItems: 'center',
  },
  inputWrapper: {
    flex: 1,
    backgroundColor: colors.surface,
    borderRadius: radius.xl,
    borderWidth: 0.5,
    borderColor: colors.separator,
  },
  input: {
    paddingHorizontal: spacing.lg,
    paddingVertical: 10,
    ...typography.body,
    color: colors.textPrimary,
    maxHeight: 120,
  },
  sendBtn: {
    width: 36,
    height: 36,
    borderRadius: radius.full,
    backgroundColor: colors.accent,
    justifyContent: 'center',
    alignItems: 'center',
  },
  micBtn: {
    width: 36,
    height: 36,
    borderRadius: radius.full,
    justifyContent: 'center',
    alignItems: 'center',
  },
  micActive: {
    backgroundColor: colors.destructive,
  },
  btnDisabled: {
    opacity: 0.4,
  },
});
