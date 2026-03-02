import React from 'react';
import { View, Image, Text, TouchableOpacity, StyleSheet } from 'react-native';
import type { PendingMedia } from '../types';

interface Props {
  media: PendingMedia;
  onRemove: () => void;
}

export default function MediaPreview({ media, onRemove }: Props) {
  const isImage = media.mime_type.startsWith('image/');

  return (
    <View style={styles.container}>
      {isImage ? (
        <Image source={{ uri: media.uri }} style={styles.image} resizeMode="cover" />
      ) : (
        <View style={styles.file}>
          <Text style={styles.fileName} numberOfLines={1}>
            {media.file_name}
          </Text>
        </View>
      )}
      <TouchableOpacity style={styles.remove} onPress={onRemove}>
        <Text style={styles.removeText}>x</Text>
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    position: 'relative',
    marginRight: 8,
  },
  image: {
    width: 80,
    height: 80,
    borderRadius: 8,
  },
  file: {
    width: 80,
    height: 80,
    borderRadius: 8,
    backgroundColor: '#2a2a4e',
    justifyContent: 'center',
    alignItems: 'center',
    padding: 8,
  },
  fileName: {
    color: '#ccc',
    fontSize: 11,
    textAlign: 'center',
  },
  remove: {
    position: 'absolute',
    top: -6,
    right: -6,
    width: 22,
    height: 22,
    borderRadius: 11,
    backgroundColor: '#ff4444',
    justifyContent: 'center',
    alignItems: 'center',
  },
  removeText: {
    color: '#fff',
    fontSize: 12,
    fontWeight: '700',
  },
});
