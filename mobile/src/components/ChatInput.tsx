import React, { useState, useRef } from 'react';
import {
  View,
  TextInput,
  TouchableOpacity,
  Text,
  StyleSheet,
  ScrollView,
  Image,
} from 'react-native';
import { Audio } from 'expo-av';
import type { PendingMedia } from '../types';
import { pickPhoto, pickDocument, pickVideo, uploadVoice } from '../services/media';

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
    } catch (e) {
      console.error('Photo pick error:', e);
    }
  };

  const handlePickDocument = async () => {
    try {
      const media = await pickDocument();
      if (media) onMediaAttached(media);
    } catch (e) {
      console.error('Document pick error:', e);
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
    } catch (e) {
      console.error('Recording start error:', e);
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
        // Auto-send voice transcript as message.
        if (result.transcript) {
          onSend(`[Voice] ${result.transcript}`);
        }
      }
    } catch (e) {
      console.error('Recording stop error:', e);
    }
  };

  return (
    <View style={styles.container}>
      {pendingMedia.length > 0 && (
        <ScrollView horizontal style={styles.mediaRow}>
          {pendingMedia.map((m) => (
            <View key={m.upload_id} style={styles.mediaPreview}>
              {m.mime_type.startsWith('image/') ? (
                <Image source={{ uri: m.uri }} style={styles.previewImage} />
              ) : (
                <View style={styles.previewFile}>
                  <Text style={styles.previewFileText} numberOfLines={1}>
                    {m.file_name}
                  </Text>
                </View>
              )}
              <TouchableOpacity
                style={styles.removeBtn}
                onPress={() => onRemoveMedia(m.upload_id)}
              >
                <Text style={styles.removeBtnText}>x</Text>
              </TouchableOpacity>
            </View>
          ))}
        </ScrollView>
      )}

      <View style={styles.inputRow}>
        <TouchableOpacity
          style={styles.iconBtn}
          onPress={handlePickPhoto}
          disabled={disabled}
        >
          <Text style={styles.iconText}>📷</Text>
        </TouchableOpacity>

        <TouchableOpacity
          style={styles.iconBtn}
          onPress={handlePickDocument}
          disabled={disabled}
        >
          <Text style={styles.iconText}>📎</Text>
        </TouchableOpacity>

        <TextInput
          ref={inputRef}
          style={styles.input}
          value={text}
          onChangeText={setText}
          placeholder="Message ALF..."
          placeholderTextColor="#666"
          multiline
          maxLength={4096}
          editable={!disabled}
          onSubmitEditing={handleSend}
          blurOnSubmit={false}
        />

        {text.trim() || pendingMedia.length > 0 ? (
          <TouchableOpacity
            style={[styles.sendBtn, disabled && styles.btnDisabled]}
            onPress={handleSend}
            disabled={disabled}
          >
            <Text style={styles.sendText}>↑</Text>
          </TouchableOpacity>
        ) : (
          <TouchableOpacity
            style={[styles.micBtn, isRecording && styles.micActive]}
            onPressIn={handleStartRecording}
            onPressOut={handleStopRecording}
            disabled={disabled}
          >
            <Text style={styles.iconText}>🎤</Text>
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    backgroundColor: '#1a1a2e',
    borderTopWidth: 1,
    borderTopColor: '#2a2a4e',
    paddingBottom: 8,
  },
  mediaRow: {
    paddingHorizontal: 12,
    paddingTop: 8,
  },
  mediaPreview: {
    marginRight: 8,
    position: 'relative',
  },
  previewImage: {
    width: 60,
    height: 60,
    borderRadius: 8,
  },
  previewFile: {
    width: 80,
    height: 60,
    borderRadius: 8,
    backgroundColor: '#2a2a4e',
    justifyContent: 'center',
    alignItems: 'center',
    padding: 4,
  },
  previewFileText: {
    color: '#ccc',
    fontSize: 10,
  },
  removeBtn: {
    position: 'absolute',
    top: -4,
    right: -4,
    backgroundColor: '#ff4444',
    borderRadius: 10,
    width: 20,
    height: 20,
    justifyContent: 'center',
    alignItems: 'center',
  },
  removeBtnText: {
    color: '#fff',
    fontSize: 12,
    fontWeight: '700',
  },
  inputRow: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    paddingHorizontal: 12,
    paddingTop: 8,
    gap: 6,
  },
  iconBtn: {
    width: 36,
    height: 36,
    justifyContent: 'center',
    alignItems: 'center',
  },
  iconText: {
    fontSize: 20,
  },
  input: {
    flex: 1,
    backgroundColor: '#2a2a4e',
    borderRadius: 20,
    paddingHorizontal: 16,
    paddingVertical: 10,
    fontSize: 16,
    color: '#e0e0e0',
    maxHeight: 120,
  },
  sendBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#6c63ff',
    justifyContent: 'center',
    alignItems: 'center',
  },
  sendText: {
    color: '#fff',
    fontSize: 20,
    fontWeight: '700',
  },
  micBtn: {
    width: 36,
    height: 36,
    justifyContent: 'center',
    alignItems: 'center',
  },
  micActive: {
    backgroundColor: '#ff4444',
    borderRadius: 18,
  },
  btnDisabled: {
    opacity: 0.5,
  },
});
