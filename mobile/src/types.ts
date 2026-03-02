export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  ts: string;
  model?: string;
  tier?: string;
  cost_usd?: number;
  session_id?: string;
  reply_to?: string;
  media?: MediaRef[];
  reactions?: Reaction[];
}

export interface MediaRef {
  upload_id: string;
  type: 'photo' | 'document' | 'video' | 'voice';
  file_name: string;
  mime_type: string;
  url?: string;
}

export interface Reaction {
  emoji: string;
  from: 'user' | 'alf';
}

export interface UploadResult {
  upload_id: string;
  file_name: string;
  mime_type: string;
  size: number;
  transcript?: string;
}

export interface ChatDoneData {
  msg_id: string;
  session_id: string;
  model: string;
  cost_usd: number;
  tier: string;
}

export interface ReactResult {
  ok: boolean;
  mirror?: string;
}

export type SSEEventType = 'thinking' | 'tool_use' | 'text' | 'reaction' | 'done' | 'error';

export interface SSEEvent {
  type: SSEEventType;
  data: any;
}

export interface StreamingState {
  isStreaming: boolean;
  phase: 'idle' | 'thinking' | 'tool_use' | 'writing';
  toolName?: string;
  accumulatedText: string;
}

export interface PendingMedia {
  upload_id: string;
  file_name: string;
  mime_type: string;
  uri: string; // local URI for preview
}
