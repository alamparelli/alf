import * as SecureStore from 'expo-secure-store';

const SERVER_URL_KEY = 'alf_server_url';
const TOKEN_KEY = 'alf_auth_token';

export async function getServerUrl(): Promise<string | null> {
  return SecureStore.getItemAsync(SERVER_URL_KEY);
}

export async function setServerUrl(url: string): Promise<void> {
  // Normalize: remove trailing slash.
  const normalized = url.replace(/\/+$/, '');
  await SecureStore.setItemAsync(SERVER_URL_KEY, normalized);
}

export async function getToken(): Promise<string | null> {
  return SecureStore.getItemAsync(TOKEN_KEY);
}

export async function setToken(token: string): Promise<void> {
  await SecureStore.setItemAsync(TOKEN_KEY, token);
}

export async function clearAuth(): Promise<void> {
  await SecureStore.deleteItemAsync(SERVER_URL_KEY);
  await SecureStore.deleteItemAsync(TOKEN_KEY);
}

export async function isConfigured(): Promise<boolean> {
  const url = await getServerUrl();
  const token = await getToken();
  return !!(url && token);
}
