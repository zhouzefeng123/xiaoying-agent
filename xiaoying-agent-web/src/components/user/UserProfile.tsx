import React, { useState, useEffect } from 'react';

interface UserProfileProps {
  userId: string;
}

interface User {
  id: string;
  avatar: string;
  nickname: string;
  bio: string;
}

const UserProfile: React.FC<UserProfileProps> = ({ userId }) => {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);

  // Simulate API call to fetch user data
  useEffect(() => {
    const fetchUser = async () => {
      try {
        setLoading(true);
        setError(null);
        
        // Simulate network delay
        await new Promise(resolve => setTimeout(resolve, 800));
        
        // Mock user data based on userId
        const mockUsers: Record<string, User> = {
          '1': {
            id: '1',
            avatar: 'https://ui-avatars.com/api/?name=John+Doe&background=0D8BCD&color=fff',
            nickname: 'JohnDoe',
            bio: 'Senior AI Engineer passionate about building intelligent systems and human-AI collaboration.',
          },
          '2': {
            id: '2',
            avatar: 'https://ui-avatars.com/api/?name=Jane+Smith&background=4F46E5&color=fff',
            nickname: 'JaneSmith',
            bio: 'UX Designer and Frontend Developer focused on creating accessible and beautiful web experiences.',
          },
          '3': {
            id: '3',
            avatar: 'https://ui-avatars.com/api/?name=Alex+Johnson&background=10B981&color=fff',
            nickname: 'AlexJohnson',
            bio: 'Full-stack developer specializing in React, TypeScript, and modern web technologies.',
          },
        };
        
        const userData = mockUsers[userId] || {
          id: userId,
          avatar: `https://ui-avatars.com/api/?name=User+${userId}&background=8B5CF6&color=fff`,
          nickname: `User${userId}`,
          bio: 'This is a sample user profile. The bio provides additional information about the user.',
        };
        
        setUser(userData);
      } catch (err) {
        setError('Failed to load user profile. Please try again later.');
        console.error('Error fetching user:', err);
      } finally {
        setLoading(false);
      }
    };

    fetchUser();
  }, [userId]);

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center p-6 bg-white rounded-xl shadow-sm border border-gray-100 max-w-md w-full">
        <div className="animate-pulse flex flex-col items-center space-y-4">
          <div className="w-24 h-24 bg-gray-200 rounded-full"></div>
          <div className="h-6 bg-gray-200 rounded w-32"></div>
          <div className="h-4 bg-gray-200 rounded w-48"></div>
          <div className="h-4 bg-gray-200 rounded w-64"></div>
          <div className="h-4 bg-gray-200 rounded w-64"></div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center p-6 bg-white rounded-xl shadow-sm border border-gray-100 max-w-md w-full">
        <div className="text-red-600 font-medium mb-2">Error</div>
        <div className="text-gray-600 text-sm text-center mb-4">{error}</div>
        <button 
          onClick={() => window.location.reload()}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors duration-200"
        >
          Try Again
        </button>
      </div>
    );
  }

  if (!user) return null;

  return (
    <div className="flex flex-col items-center justify-center p-6 bg-white rounded-xl shadow-sm border border-gray-100 max-w-md w-full">
      <div className="mb-4">
        <img 
          src={user.avatar} 
          alt={user.nickname} 
          className="w-24 h-24 rounded-full object-cover border-4 border-white shadow-md"
        />
      </div>
      
      <div className="text-center mb-4">
        <h2 className="text-xl font-bold text-gray-900 mb-1">{user.nickname}</h2>
        <p className="text-gray-600 text-sm">User ID: {user.id}</p>
      </div>
      
      <div className="w-full bg-gray-50 rounded-lg p-4 border border-gray-200">
        <p className="text-gray-700 leading-relaxed">{user.bio}</p>
      </div>
    </div>
  );
};

export default UserProfile;