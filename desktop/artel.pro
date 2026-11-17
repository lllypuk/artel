QT += core gui qml quick quickcontrols2 network
CONFIG += c++17 release warn_on
QMAKE_CXXFLAGS += -Wextra -Werror
TARGET = artel
TEMPLATE = app
SOURCES += src/main.cpp src/apiclient.cpp
HEADERS += src/apiclient.h
RESOURCES += src/resources.qrc
