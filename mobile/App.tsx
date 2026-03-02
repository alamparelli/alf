import React, { useEffect, useState } from 'react';
import { NavigationContainer } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { StatusBar } from 'expo-status-bar';
import { Text } from 'react-native';

import ChatScreen from './src/screens/ChatScreen';
import CCScreen from './src/screens/CCScreen';
import SetupScreen from './src/screens/SetupScreen';
import { isConfigured } from './src/services/auth';

const Tab = createBottomTabNavigator();

export default function App() {
  const [configured, setConfigured] = useState<boolean | null>(null);

  useEffect(() => {
    isConfigured().then(setConfigured);
  }, []);

  if (configured === null) return null; // loading

  if (!configured) {
    return (
      <>
        <StatusBar style="light" />
        <SetupScreen onComplete={() => setConfigured(true)} />
      </>
    );
  }

  return (
    <>
      <StatusBar style="light" />
      <NavigationContainer>
        <Tab.Navigator
          screenOptions={{
            headerStyle: { backgroundColor: '#1a1a2e' },
            headerTintColor: '#e0e0e0',
            tabBarStyle: { backgroundColor: '#1a1a2e', borderTopColor: '#2a2a4e' },
            tabBarActiveTintColor: '#6c63ff',
            tabBarInactiveTintColor: '#888',
          }}
        >
          <Tab.Screen
            name="Chat"
            component={ChatScreen}
            options={{
              tabBarIcon: ({ color }) => <Text style={{ color, fontSize: 20 }}>💬</Text>,
            }}
          />
          <Tab.Screen
            name="Control Center"
            component={CCScreen}
            options={{
              tabBarIcon: ({ color }) => <Text style={{ color, fontSize: 20 }}>⚙️</Text>,
            }}
          />
        </Tab.Navigator>
      </NavigationContainer>
    </>
  );
}
