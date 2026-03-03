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

/** Parse SSE lines from a text chunk, returns unconsumed remainder. */
function parseSSEChunk(
  text: string,
  onEvent: (event: SSEEvent) => void,
): string {
  const lines = text.split('\n');
  const remainder = lines.pop() || '';

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

  return remainder;
}

/** Send a chat message and stream SSE events via XMLHttpRequest (RN compatible). */
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

  return new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${url}/api/chat`);
    xhr.setRequestHeader('Content-Type', 'application/json');
    for (const [key, value] of Object.entries(h)) {
      xhr.setRequestHeader(key, value);
    }

    let lastIndex = 0;
    let buffer = '';

    xhr.onprogress = () => {
      const newText = xhr.responseText.slice(lastIndex);
      lastIndex = xhr.responseText.length;
      buffer += newText;
      buffer = parseSSEChunk(buffer, onEvent);
    };

    xhr.onload = () => {
      if (xhr.status !== 200) {
        reject(new Error(`Chat error: ${xhr.status} ${xhr.responseText}`));
        return;
      }
      // Parse any remaining buffered data.
      if (buffer) {
        parseSSEChunk(buffer + '\n', onEvent);
      }
      resolve();
    };

    xhr.onerror = () => {
      reject(new Error('Network error'));
    };

    xhr.ontimeout = () => {
      reject(new Error('Request timed out'));
    };

    xhr.timeout = 300000; // 5 min for long Claude responses
    xhr.send(body);
  });
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

/** Check if the server is reachable and auth is valid. Throws with details on failure. */
export async function healthCheck(): Promise<boolean> {
  const url = await baseUrl();
  const h = await headers();
  const response = await fetch(`${url}/api/status`, { headers: h });
  if (!response.ok) {
    const text = await response.text().catch(() => '');
    throw new Error(`${response.status}: ${text || response.statusText}`);
  }
  return true;
}
