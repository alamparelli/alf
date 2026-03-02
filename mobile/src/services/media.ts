import * as ImagePicker from 'expo-image-picker';
import * as DocumentPicker from 'expo-document-picker';
import { uploadMedia } from './api';
import type { UploadResult, PendingMedia } from '../types';

/** Pick a photo from the library or camera. */
export async function pickPhoto(): Promise<PendingMedia | null> {
  const result = await ImagePicker.launchImageLibraryAsync({
    mediaTypes: ['images'],
    quality: 0.8,
    allowsEditing: false,
  });

  if (result.canceled || !result.assets[0]) return null;

  const asset = result.assets[0];
  const fileName = asset.fileName || 'photo.jpg';
  const mimeType = asset.mimeType || 'image/jpeg';

  const uploadResult = await uploadMedia(asset.uri, fileName, mimeType, 'photo');
  return {
    upload_id: uploadResult.upload_id,
    file_name: uploadResult.file_name,
    mime_type: uploadResult.mime_type,
    uri: asset.uri,
  };
}

/** Pick a video from the library. */
export async function pickVideo(): Promise<PendingMedia | null> {
  const result = await ImagePicker.launchImageLibraryAsync({
    mediaTypes: ['videos'],
    quality: 0.8,
  });

  if (result.canceled || !result.assets[0]) return null;

  const asset = result.assets[0];
  const fileName = asset.fileName || 'video.mp4';
  const mimeType = asset.mimeType || 'video/mp4';

  const uploadResult = await uploadMedia(asset.uri, fileName, mimeType, 'video');
  return {
    upload_id: uploadResult.upload_id,
    file_name: uploadResult.file_name,
    mime_type: uploadResult.mime_type,
    uri: asset.uri,
  };
}

/** Pick a document (PDF, txt, etc). */
export async function pickDocument(): Promise<PendingMedia | null> {
  const result = await DocumentPicker.getDocumentAsync({
    type: '*/*',
    copyToCacheDirectory: true,
  });

  if (result.canceled || !result.assets[0]) return null;

  const asset = result.assets[0];
  const uploadResult = await uploadMedia(
    asset.uri,
    asset.name,
    asset.mimeType || 'application/octet-stream',
    'document',
  );
  return {
    upload_id: uploadResult.upload_id,
    file_name: uploadResult.file_name,
    mime_type: uploadResult.mime_type,
    uri: asset.uri,
  };
}

/** Upload a voice recording file. */
export async function uploadVoice(uri: string): Promise<UploadResult> {
  return uploadMedia(uri, 'voice.m4a', 'audio/mp4', 'voice');
}
