import { getServerUrl, getToken } from './auth';
import type { ChatMessage, UploadResult, ReactResult, SSEEvent, ChatDoneData } from '../types';

async function baseUrl(): Promise<string> {
  const url = await getServerUrl();
  if (!url) throw new Error('Server URL not configured');
  return url;
}

async function headers(): Promise<Record<string, string>> {
  const token = await getToken();
  return {
    Authorization: `Bearer ${token}`,
  };
}

/** Send a chat message and stream SSE events. */
export async function sendMessage(
  message: string,
  options: {
    reply_to?: string;
    media_ids?: string[];
    model?: string;
  },
  onEvent: (event: SSEEvent) => void,
): Promise<void> {
  const url = await baseUrl();
  const h = await headers();

  const body = JSON.stringify({
    message,
    reply_to: options.reply_to,
    media_ids: options.media_ids,
    model: options.model,
  });

  const response = await fetch(`${url}/api/chat`, {
    method: 'POST',
    headers: { ...h, 'Content-Type': 'application/json' },
    body,
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Chat error: ${response.status} ${text}`);
  }

  if (!response.body) {
    throw new Error('No response body for SSE');
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';

    let currentEventType = '';
    for (const line of lines) {
      if (line.startsWith('event: ')) {
        currentEventType = line.slice(7).trim();
      } else if (line.startsWith('data: ') && currentEventType) {
        try {
          const data = JSON.parse(line.slice(6));
          onEvent({ type: currentEventType as SSEEvent['type'], data });
        } catch {
          // Skip malformed JSON.
        }
        currentEventType = '';
      }
    }
  }
}

/** Upload a media file. */
export async function uploadMedia(
  uri: string,
  fileName: string,
  mimeType: string,
  mediaType: 'photo' | 'document' | 'video' | 'voice',
): Promise<UploadResult> {
  const url = await baseUrl();
  const h = await headers();

  const formData = new FormData();
  formData.append('file', {
    uri,
    name: fileName,
    type: mimeType,
  } as any);
  formData.append('type', mediaType);

  const response = await fetch(`${url}/api/chat/upload`, {
    method: 'POST',
    headers: h,
    body: formData,
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Upload error: ${response.status} ${text}`);
  }

  return response.json();
}

/** Send a reaction on a message. */
export async function sendReaction(msgId: string, emoji: string): Promise<ReactResult> {
  const url = await baseUrl();
  const h = await headers();

  const response = await fetch(`${url}/api/chat/react`, {
    method: 'POST',
    headers: { ...h, 'Content-Type': 'application/json' },
    body: JSON.stringify({ msg_id: msgId, emoji }),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Reaction error: ${response.status} ${text}`);
  }

  return response.json();
}

/** Fetch chat history. */
export async function fetchHistory(
  limit = 50,
  before?: string,
): Promise<ChatMessage[]> {
  const url = await baseUrl();
  const h = await headers();

  const params = new URLSearchParams({ limit: String(limit) });
  if (before) params.set('before', before);

  const response = await fetch(`${url}/api/chat?${params}`, {
    headers: h,
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`History error: ${response.status} ${text}`);
  }

  return response.json();
}

/** Build full media URL for display. */
export async function mediaUrl(uploadId: string): Promise<string> {
  const url = await baseUrl();
  const token = await getToken();
  return `${url}/api/chat/media/${uploadId}?token=${token}`;
}

/** Check if the server is reachable and auth is valid. */
export async function healthCheck(): Promise<boolean> {
  try {
    const url = await baseUrl();
    const h = await headers();
    const response = await fetch(`${url}/api/status`, { headers: h });
    return response.ok;
  } catch {
    return false;
  }
}
